package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/Keldrik/pcast/internal/model"
)

// ListEpisodes queries the cached catalog.
func (a *App) ListEpisodes(ctx context.Context, selector string, f model.EpisodeFilter) (model.EpisodesResult, error) {
	if err := f.Validate(); err != nil {
		return model.EpisodesResult{}, err
	}
	if selector != "" {
		p, err := a.ResolvePodcast(ctx, selector)
		if err != nil {
			return model.EpisodesResult{}, err
		}
		id := p.ID
		f.PodcastID = &id
	}
	eps, err := a.Store.QueryEpisodes(ctx, f)
	if err != nil {
		return model.EpisodesResult{}, err
	}
	if eps == nil {
		eps = []model.Episode{}
	}
	return model.EpisodesResult{Episodes: eps}, nil
}

// GetEpisode returns one episode by ID string.
func (a *App) GetEpisode(ctx context.Context, idStr string) (model.EpisodeResult, error) {
	id, err := parseEpisodeID(idStr)
	if err != nil {
		return model.EpisodeResult{}, err
	}
	ep, err := a.Store.GetEpisode(ctx, id)
	if err != nil {
		return model.EpisodeResult{}, err
	}
	return model.EpisodeResult{Episode: ep}, nil
}

// Mark sets played/unplayed state.
func (a *App) Mark(ctx context.Context, idStr, state string) (model.MarkResult, error) {
	id, err := parseEpisodeID(idStr)
	if err != nil {
		return model.MarkResult{}, err
	}
	state = strings.ToLower(strings.TrimSpace(state))
	var result model.MarkResult
	err = a.locker().WithLock(ctx, func(ctx context.Context) error {
		var ep model.Episode
		var err error
		switch state {
		case "played":
			ep, err = a.Store.MarkPlayed(ctx, id, a.now())
		case "unplayed":
			ep, err = a.Store.MarkUnplayed(ctx, id)
		default:
			return model.InvalidArgumentf("state must be played or unplayed, got %q", state)
		}
		if err != nil {
			return err
		}
		result = model.MarkResult{Episode: ep}
		return nil
	})
	return result, err
}

// Play streams an episode through the configured player.
func (a *App) Play(ctx context.Context, idStr string, opts model.PlaybackOpts) (model.PlayResult, error) {
	id, err := parseEpisodeID(idStr)
	if err != nil {
		return model.PlayResult{}, err
	}
	ep, err := a.Store.GetEpisode(ctx, id)
	if err != nil {
		return model.PlayResult{}, err
	}
	if a.Player == nil {
		return model.PlayResult{}, model.PlayerUnavailable("player is not configured")
	}
	ref, err := a.Player.Resolve(opts.Player, opts.PlayerArgs)
	if err != nil {
		return model.PlayResult{}, err
	}
	outcome, err := a.Player.Play(ctx, ref, ep.EnclosureURL)
	if err != nil {
		return model.PlayResult{
			Episode:    ep,
			Player:     ref.Name,
			Marked:     false,
			ExitStatus: outcome.ExitStatus,
		}, err
	}

	marked := false
	if !opts.NoMarkPlayed {
		err = a.locker().WithLock(ctx, func(ctx context.Context) error {
			updated, err := a.Store.RecordPlayback(ctx, id, a.now())
			if err != nil {
				return err
			}
			ep = updated
			marked = true
			return nil
		})
		if err != nil {
			return model.PlayResult{Episode: ep, Player: outcome.Player, Marked: false, ExitStatus: outcome.ExitStatus}, err
		}
	}
	return model.PlayResult{
		Episode:    ep,
		Player:     outcome.Player,
		Marked:     marked,
		ExitStatus: outcome.ExitStatus,
	}, nil
}

func parseEpisodeID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || !isAllDigits(s) {
		return 0, model.InvalidArgumentf("episode id must be a positive integer, got %q", s)
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, model.InvalidArgumentf("episode id must be a positive integer, got %q", s)
	}
	return id, nil
}
