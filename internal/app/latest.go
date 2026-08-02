package app

import (
	"context"
	"errors"
	"sync"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/store"
)

// fetchOutcome is a network-only feed check result (no DB writes).
type fetchOutcome struct {
	podcast model.Podcast
	parsed  model.ParsedFeed
	err     error
}

// Latest checks feeds and returns pending episodes while holding the
// application lock across selection, fetching, applying, and reading state.
func (a *App) Latest(ctx context.Context, selector string) (model.LatestResult, error) {
	return a.LatestLocked(ctx, selector, nil)
}

// LatestLocked holds the application lock across the complete check. This
// prevents a slow, stale response from overwriting newer feed metadata.
// writeFn runs before acknowledgement; a nil writeFn leaves episodes pending.
func (a *App) LatestLocked(ctx context.Context, selector string, writeFn func(model.LatestResult) error) (model.LatestResult, error) {
	var result model.LatestResult
	err := a.locker().WithLock(ctx, func(ctx context.Context) error {
		podcasts, err := a.podcastsForLatest(ctx, selector)
		if err != nil {
			return err
		}
		outcomes := a.fetchAll(ctx, podcasts)
		result, err = a.applyOutcomes(ctx, podcasts, selector, outcomes)
		if err != nil {
			return err
		}
		if writeFn == nil {
			return nil
		}
		if err := writeFn(result); err != nil {
			return model.Internal("write latest output", err)
		}
		return a.AcknowledgeLatest(ctx, result.Episodes)
	})
	return result, err
}

func (a *App) podcastsForLatest(ctx context.Context, selector string) ([]model.Podcast, error) {
	if selector != "" {
		p, err := a.ResolvePodcast(ctx, selector)
		if err != nil {
			return nil, err
		}
		return []model.Podcast{p}, nil
	}
	return a.Store.ListPodcasts(ctx)
}

func (a *App) fetchAll(ctx context.Context, podcasts []model.Podcast) []fetchOutcome {
	out := make([]fetchOutcome, len(podcasts))
	// Default to cancelled; workers overwrite when a job actually runs.
	for i, p := range podcasts {
		out[i] = fetchOutcome{podcast: p, err: context.Canceled}
	}
	if len(podcasts) == 0 {
		return out
	}

	workers := a.concurrency()
	if workers > len(podcasts) {
		workers = len(podcasts)
	}
	if workers < 1 {
		workers = 1
	}

	type job struct {
		i int
		p model.Podcast
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				parsed, err := a.fetchOne(ctx, j.p)
				out[j.i] = fetchOutcome{podcast: j.p, parsed: parsed, err: err}
			}
		}()
	}

send:
	for i, p := range podcasts {
		select {
		case <-ctx.Done():
			break send
		case jobs <- job{i: i, p: p}:
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

func (a *App) fetchOne(ctx context.Context, p model.Podcast) (model.ParsedFeed, error) {
	if err := ctx.Err(); err != nil {
		return model.ParsedFeed{}, err
	}
	return a.Feeds.Fetch(ctx, FetchOpts{
		URL:          p.FeedURL,
		ETag:         p.ETag,
		LastModified: p.LastModified,
	})
}

func (a *App) applyOutcomes(ctx context.Context, podcasts []model.Podcast, selector string, outcomes []fetchOutcome) (model.LatestResult, error) {
	byID := make(map[int64]fetchOutcome, len(outcomes))
	for _, o := range outcomes {
		if o.podcast.ID != 0 {
			byID[o.podcast.ID] = o
		}
	}

	checks := make([]model.FeedCheck, 0, len(podcasts))
	failures := make([]model.FeedFailure, 0)

	for _, p := range podcasts {
		o, ok := byID[p.ID]
		if !ok {
			fail := model.FeedFailure{
				PodcastID: p.ID,
				Code:      model.CodeFeedUnavailable,
				Message:   "check cancelled",
			}
			checks = append(checks, model.FeedCheck{
				PodcastID: p.ID,
				Status:    model.CheckStatusFailed,
				NewCount:  0,
				ErrorCode: strPtr(fail.Code),
				Message:   strPtr(fail.Message),
			})
			failures = append(failures, fail)
			continue
		}
		check, failure, err := a.applyOne(ctx, p, o)
		if err != nil {
			return model.LatestResult{}, err
		}
		checks = append(checks, check)
		if failure != nil {
			failures = append(failures, *failure)
		}
	}

	var scopeID *int64
	if selector != "" && len(podcasts) == 1 {
		scopeID = &podcasts[0].ID
	}
	pending, err := a.Store.ListPendingEpisodes(ctx, scopeID)
	if err != nil {
		return model.LatestResult{}, err
	}
	model.SortEpisodesLatest(pending)
	if pending == nil {
		pending = []model.Episode{}
	}
	if failures == nil {
		failures = []model.FeedFailure{}
	}
	return model.LatestResult{
		Partial:  len(failures) > 0,
		Checks:   checks,
		Episodes: pending,
		Failures: failures,
	}, nil
}

func (a *App) applyOne(ctx context.Context, p model.Podcast, o fetchOutcome) (model.FeedCheck, *model.FeedFailure, error) {
	now := a.now()
	if o.err != nil {
		if (errors.Is(o.err, context.Canceled) || errors.Is(o.err, context.DeadlineExceeded)) && ctx.Err() != nil {
			msg := "check cancelled"
			code := model.CodeFeedUnavailable
			return model.FeedCheck{
				PodcastID: p.ID,
				Status:    model.CheckStatusFailed,
				NewCount:  0,
				ErrorCode: &code,
				Message:   &msg,
			}, &model.FeedFailure{PodcastID: p.ID, Code: code, Message: msg}, nil
		}
		code := model.CodeOf(o.err)
		if code == "" || code == model.CodeInternalError {
			code = model.CodeFeedUnavailable
		}
		msg := "feed check failed"
		var ae *model.Error
		if errors.Is(o.err, context.DeadlineExceeded) {
			msg = "request timed out"
		} else if errors.Is(o.err, context.Canceled) {
			msg = "check cancelled"
		} else if errors.As(o.err, &ae) {
			msg = ae.Message
		}
		var status *int
		if o.parsed.HTTPStatus != 0 {
			status = &o.parsed.HTTPStatus
		}
		check := model.FeedCheck{
			PodcastID:  p.ID,
			Status:     model.CheckStatusFailed,
			HTTPStatus: status,
			NewCount:   0,
			ErrorCode:  &code,
			Message:    &msg,
		}
		if err := a.Store.ApplyCheckFailure(ctx, p.ID, status, msg, now); err != nil {
			return check, nil, err
		}
		return check, &model.FeedFailure{PodcastID: p.ID, Code: code, Message: msg}, nil
	}

	parsed := o.parsed
	if parsed.NotModified {
		st := parsed.HTTPStatus
		check := model.FeedCheck{
			PodcastID:  p.ID,
			Status:     model.CheckStatusOK,
			HTTPStatus: &st,
			NewCount:   0,
		}
		if err := a.Store.ApplyNotModified(ctx, p.ID, parsed.HTTPStatus, now); err != nil {
			return check, nil, err
		}
		return check, nil, nil
	}

	items := make([]store.BaselineEpisode, 0, len(parsed.Episodes))
	for _, ep := range parsed.Episodes {
		items = append(items, toBaseline(ep))
	}
	n, err := a.Store.ApplyCheck(ctx, p.ID, parsed.ResolvedURL, parsed.Title, parsed.Author, parsed.Description, parsed.ETag, parsed.LastModified, parsed.HTTPStatus, items, now)
	st := parsed.HTTPStatus
	check := model.FeedCheck{
		PodcastID:  p.ID,
		Status:     model.CheckStatusOK,
		HTTPStatus: &st,
		NewCount:   n,
	}
	if err != nil {
		return check, nil, err
	}
	return check, nil, nil
}

// AcknowledgeLatest marks the given episode IDs as announced.
func (a *App) AcknowledgeLatest(ctx context.Context, episodes []model.Episode) error {
	if len(episodes) == 0 {
		return nil
	}
	ids := make([]int64, len(episodes))
	for i, e := range episodes {
		ids[i] = e.ID
	}
	return a.Store.AcknowledgeEpisodes(ctx, ids, a.now())
}

func strPtr(s string) *string { return &s }
