package app

import (
	"context"
	"strings"

	"github.com/Keldrik/pcast/internal/feed"
	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/store"
)

// Add subscribes to a feed URL, baselining current episodes.
// Network fetch runs outside the app lock; DB writes run under the lock.
func (a *App) Add(ctx context.Context, feedURL string, alias string) (model.AddResult, error) {
	norm, err := feed.NormalizeURL(feedURL)
	if err != nil {
		return model.AddResult{}, err
	}

	var aliasPtr *string
	if alias != "" {
		alias = strings.TrimSpace(alias)
		if err := store.ValidateAlias(alias); err != nil {
			return model.AddResult{}, err
		}
		aliasPtr = &alias
	}

	// Fast local duplicate check without holding the lock during network I/O.
	if existing, ok, err := a.Store.FindPodcastByURL(ctx, norm); err != nil {
		return model.AddResult{}, err
	} else if ok {
		return a.addExisting(ctx, existing, aliasPtr)
	}

	if aliasPtr != nil {
		exists, err := a.Store.AliasExists(ctx, *aliasPtr, 0)
		if err != nil {
			return model.AddResult{}, err
		}
		if exists {
			return model.AddResult{}, model.InvalidArgumentf("alias %q is already in use", *aliasPtr)
		}
	}

	parsed, err := a.Feeds.Fetch(ctx, FetchOpts{URL: norm})
	if err != nil {
		return model.AddResult{}, err
	}
	if parsed.NotModified {
		return model.AddResult{}, model.InvalidFeed("feed returned 304 on initial fetch", nil)
	}

	if parsed.ResolvedURL != "" && parsed.ResolvedURL != norm {
		if existing, ok, err := a.Store.FindPodcastByURL(ctx, parsed.ResolvedURL); err != nil {
			return model.AddResult{}, err
		} else if ok {
			return a.addExisting(ctx, existing, aliasPtr)
		}
	}

	var result model.AddResult
	err = a.locker().WithLock(ctx, func(ctx context.Context) error {
		// Re-check under lock (concurrent add).
		if existing, ok, err := a.Store.FindPodcastByURL(ctx, norm); err != nil {
			return err
		} else if ok {
			r, err := a.addExisting(ctx, existing, aliasPtr)
			result = r
			return err
		}
		if parsed.ResolvedURL != "" && parsed.ResolvedURL != norm {
			if existing, ok, err := a.Store.FindPodcastByURL(ctx, parsed.ResolvedURL); err != nil {
				return err
			} else if ok {
				r, err := a.addExisting(ctx, existing, aliasPtr)
				result = r
				return err
			}
		}

		if aliasPtr != nil {
			exists, err := a.Store.AliasExists(ctx, *aliasPtr, 0)
			if err != nil {
				return err
			}
			if exists {
				return model.InvalidArgumentf("alias %q is already in use", *aliasPtr)
			}
		}

		status := parsed.HTTPStatus
		episodes := make([]store.BaselineEpisode, 0, len(parsed.Episodes))
		for _, ep := range parsed.Episodes {
			episodes = append(episodes, toBaseline(ep))
		}

		pod, n, err := a.Store.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
			FeedURL:     norm,
			ResolvedURL: parsed.ResolvedURL,
			Alias:       aliasPtr,
			Title:       parsed.Title,
			Author:      parsed.Author,
			Description: parsed.Description,
			ETag:        parsed.ETag,
			LastMod:     parsed.LastModified,
			HTTPStatus:  &status,
			Episodes:    episodes,
			Now:         a.now(),
		})
		if err != nil {
			// Unique race on URL/alias: surface as existing when possible.
			if existing, ok, findErr := a.Store.FindPodcastByURL(ctx, norm); findErr == nil && ok {
				r, err2 := a.addExisting(ctx, existing, aliasPtr)
				result = r
				return err2
			}
			if parsed.ResolvedURL != "" {
				if existing, ok, findErr := a.Store.FindPodcastByURL(ctx, parsed.ResolvedURL); findErr == nil && ok {
					r, err2 := a.addExisting(ctx, existing, aliasPtr)
					result = r
					return err2
				}
			}
			return err
		}
		result = model.AddResult{
			Podcast:       pod,
			Created:       true,
			EpisodeCount:  n,
			ImportedCount: n,
		}
		return nil
	})
	return result, err
}

func (a *App) addExisting(ctx context.Context, existing model.Podcast, alias *string) (model.AddResult, error) {
	if alias != nil {
		exists, err := a.Store.AliasExists(ctx, *alias, existing.ID)
		if err != nil {
			return model.AddResult{}, err
		}
		if exists {
			return model.AddResult{}, model.InvalidArgumentf("alias %q is already in use", *alias)
		}
	}
	// Find*/Get paths attach EpisodeCount; use that for the add envelope.
	return model.AddResult{
		Podcast: existing, Created: false,
		EpisodeCount: existing.EpisodeCount, ImportedCount: 0,
	}, nil
}

func toBaseline(ep model.ParsedEpisode) store.BaselineEpisode {
	return store.BaselineEpisode{
		IdentityKey:     ep.IdentityKey,
		GUID:            ep.GUID,
		Title:           ep.Title,
		Description:     ep.Description,
		PublishedAt:     ep.PublishedAt,
		DurationSeconds: ep.DurationSeconds,
		EnclosureURL:    ep.EnclosureURL,
		MediaType:       ep.MediaType,
		MediaLength:     ep.MediaLength,
	}
}

// List returns all subscriptions from local state.
func (a *App) List(ctx context.Context) (model.ListResult, error) {
	pods, err := a.Store.ListPodcasts(ctx)
	if err != nil {
		return model.ListResult{}, err
	}
	if pods == nil {
		pods = []model.Podcast{}
	}
	return model.ListResult{Podcasts: pods}, nil
}

// Remove deletes a subscription and its episodes.
func (a *App) Remove(ctx context.Context, selector string) (model.RemoveResult, error) {
	var result model.RemoveResult
	err := a.locker().WithLock(ctx, func(ctx context.Context) error {
		p, err := a.ResolvePodcast(ctx, selector)
		if err != nil {
			return err
		}
		if err := a.Store.DeletePodcast(ctx, p.ID); err != nil {
			return err
		}
		result = model.RemoveResult{Podcast: p}
		return nil
	})
	return result, err
}
