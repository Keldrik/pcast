package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/store"
)

type jobResult struct {
	podcastID int64
	check     model.FeedCheck
	failure   *model.FeedFailure
}

// Latest checks feeds and returns pending episodes. The caller must write output
// then call AcknowledgeLatest. The application lock is held for the whole operation
// when using LatestAndAck with a successful writer.
func (a *App) Latest(ctx context.Context, selector string) (model.LatestResult, error) {
	var podcasts []model.Podcast
	var err error
	if selector != "" {
		p, err := a.ResolvePodcast(ctx, selector)
		if err != nil {
			return model.LatestResult{}, err
		}
		podcasts = []model.Podcast{p}
	} else {
		podcasts, err = a.Store.ListPodcasts(ctx)
		if err != nil {
			return model.LatestResult{}, err
		}
	}

	workers := a.concurrency()
	if workers > len(podcasts) {
		workers = len(podcasts)
	}
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan model.Podcast)
	results := make(chan jobResult, len(podcasts))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				results <- a.checkOne(ctx, p)
			}
		}()
	}

	go func() {
		for _, p := range podcasts {
			select {
			case <-ctx.Done():
				// still need to drain? close after sending what we can
			case jobs <- p:
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	checks := make([]model.FeedCheck, 0, len(podcasts))
	failures := make([]model.FeedFailure, 0)
	// Collect by podcast ID for deterministic order later.
	byID := make(map[int64]jobResult, len(podcasts))
	for jr := range results {
		byID[jr.podcastID] = jr
	}

	// Deterministic order by podcast ID ascending.
	for _, p := range podcasts {
		jr, ok := byID[p.ID]
		if !ok {
			// cancelled before work
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
		checks = append(checks, jr.check)
		if jr.failure != nil {
			failures = append(failures, *jr.failure)
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

	// Annotate new_count from pending per podcast after checks.
	// new_count should reflect newly inserted this round; we tracked in check.
	partial := len(failures) > 0
	if pending == nil {
		pending = []model.Episode{}
	}
	return model.LatestResult{
		Partial:  partial,
		Checks:   checks,
		Episodes: pending,
		Failures: failures,
	}, nil
}

func (a *App) checkOne(ctx context.Context, p model.Podcast) jobResult {
	jr := jobResult{podcastID: p.ID}
	if err := ctx.Err(); err != nil {
		msg := "check cancelled"
		jr.check = model.FeedCheck{
			PodcastID: p.ID,
			Status:    model.CheckStatusFailed,
			NewCount:  0,
			ErrorCode: strPtr(model.CodeFeedUnavailable),
			Message:   &msg,
		}
		jr.failure = &model.FeedFailure{PodcastID: p.ID, Code: model.CodeFeedUnavailable, Message: msg}
		return jr
	}

	parsed, err := a.Feeds.Fetch(ctx, FetchOpts{
		URL:          p.FeedURL,
		ETag:         p.ETag,
		LastModified: p.LastModified,
	})
	now := a.now()
	if err != nil {
		code := model.CodeOf(err)
		if code == "" || code == model.CodeInternalError {
			code = model.CodeFeedUnavailable
		}
		msg := err.Error()
		// Prefer concise message without nested wraps when typed.
		if ae, ok := err.(*model.Error); ok {
			msg = ae.Message
			if ae.Err != nil {
				msg = fmt.Sprintf("%s: %v", ae.Message, ae.Err)
			}
		}
		var status *int
		if parsed.HTTPStatus != 0 {
			status = &parsed.HTTPStatus
		}
		_ = a.Store.ApplyCheckFailure(ctx, p.ID, status, msg, now)
		jr.check = model.FeedCheck{
			PodcastID:  p.ID,
			Status:     model.CheckStatusFailed,
			HTTPStatus: status,
			NewCount:   0,
			ErrorCode:  &code,
			Message:    &msg,
		}
		jr.failure = &model.FeedFailure{PodcastID: p.ID, Code: code, Message: msg}
		return jr
	}

	if parsed.NotModified {
		_ = a.Store.ApplyNotModified(ctx, p.ID, parsed.HTTPStatus, now)
		st := parsed.HTTPStatus
		jr.check = model.FeedCheck{
			PodcastID:  p.ID,
			Status:     model.CheckStatusOK,
			HTTPStatus: &st,
			NewCount:   0,
		}
		return jr
	}

	items := make([]store.BaselineEpisode, 0, len(parsed.Episodes))
	for _, ep := range parsed.Episodes {
		items = append(items, toBaseline(ep))
	}
	n, err := a.Store.ApplyCheck(ctx, p.ID, parsed.ResolvedURL, parsed.Title, parsed.Author, parsed.Description, parsed.ETag, parsed.LastModified, parsed.HTTPStatus, items, now)
	if err != nil {
		msg := err.Error()
		code := model.CodeStorageError
		_ = a.Store.ApplyCheckFailure(ctx, p.ID, &parsed.HTTPStatus, msg, now)
		jr.check = model.FeedCheck{
			PodcastID:  p.ID,
			Status:     model.CheckStatusFailed,
			HTTPStatus: &parsed.HTTPStatus,
			NewCount:   0,
			ErrorCode:  &code,
			Message:    &msg,
		}
		jr.failure = &model.FeedFailure{PodcastID: p.ID, Code: code, Message: msg}
		return jr
	}
	st := parsed.HTTPStatus
	jr.check = model.FeedCheck{
		PodcastID:  p.ID,
		Status:     model.CheckStatusOK,
		HTTPStatus: &st,
		NewCount:   n,
	}
	return jr
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

// LatestLocked runs latest under the application lock and acknowledges after writeOK.
// writeFn must write the complete response; acknowledgement runs only if writeFn returns nil.
func (a *App) LatestLocked(ctx context.Context, selector string, writeFn func(model.LatestResult) error) (model.LatestResult, error) {
	var result model.LatestResult
	err := a.locker().WithLock(ctx, func(ctx context.Context) error {
		var err error
		result, err = a.Latest(ctx, selector)
		if err != nil {
			return err
		}
		if err := writeFn(result); err != nil {
			// Leave pending; return write error as internal.
			return model.Internal("write latest output", err)
		}
		if err := a.AcknowledgeLatest(ctx, result.Episodes); err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func strPtr(s string) *string { return &s }
