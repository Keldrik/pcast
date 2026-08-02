package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/Keldrik/pcast/internal/feed"
	"github.com/Keldrik/pcast/internal/model"
)

// ResolvePodcast resolves a podcast selector with documented precedence.
func (a *App) ResolvePodcast(ctx context.Context, selector string) (model.Podcast, error) {
	sel := strings.TrimSpace(selector)
	if sel == "" {
		return model.Podcast{}, model.InvalidArgument("podcast selector is required")
	}

	// 1. Digits only → ID
	if isAllDigits(sel) {
		id, err := strconv.ParseInt(sel, 10, 64)
		if err != nil || id <= 0 {
			return model.Podcast{}, model.NotFoundf("no podcast with id %s", sel)
		}
		return a.Store.GetPodcastByID(ctx, id)
	}

	// 2. URL → normalize and match submitted/resolved
	if strings.HasPrefix(strings.ToLower(sel), "http://") || strings.HasPrefix(strings.ToLower(sel), "https://") {
		norm, err := feed.NormalizeURL(sel)
		if err != nil {
			return model.Podcast{}, err
		}
		p, ok, err := a.Store.FindPodcastByURL(ctx, norm)
		if err != nil {
			return model.Podcast{}, err
		}
		if !ok {
			return model.Podcast{}, model.NotFoundf("no podcast matches URL %q", norm)
		}
		return p, nil
	}

	// 3. Alias first (case-insensitive), then exact title
	if p, ok, err := a.Store.FindPodcastByAlias(ctx, sel); err != nil {
		return model.Podcast{}, err
	} else if ok {
		return p, nil
	}

	matches, err := a.Store.FindPodcastsByTitle(ctx, sel)
	if err != nil {
		return model.Podcast{}, err
	}
	switch len(matches) {
	case 0:
		return model.Podcast{}, model.NotFoundf("no podcast matches %q", sel)
	case 1:
		return matches[0], nil
	default:
		ids := make([]int64, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return model.Podcast{}, model.AmbiguousSelector(
			fmt.Sprintf("multiple podcasts match title %q", sel),
			ids,
		)
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
