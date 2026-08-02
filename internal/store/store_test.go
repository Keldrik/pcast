package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "pcast.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pcast.db")
	ctx := context.Background()
	s1, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s1.SchemaVersion(ctx)
	if err != nil || v1 != 1 {
		t.Fatalf("v1=%d err=%v", v1, err)
	}
	_ = s1.Close()

	s2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	v2, _ := s2.SchemaVersion(ctx)
	if v2 != 1 {
		t.Fatalf("v2=%d", v2)
	}
	fk, err := s2.ForeignKeysEnabled(ctx)
	if err != nil || !fk {
		t.Fatalf("fk=%v err=%v", fk, err)
	}
}

func TestCreateListDeletePodcast(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	pub := now.Add(-time.Hour)
	p, n, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL:     "https://example.com/feed.xml",
		ResolvedURL: "https://example.com/feed.xml",
		Alias:       strp("daily"),
		Title:       "Daily Show",
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "guid:1", GUID: strp("1"), Title: "Ep1", EnclosureURL: "https://cdn.example.com/1.mp3", PublishedAt: &pub},
		},
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 || n != 1 {
		t.Fatalf("id=%d n=%d", p.ID, n)
	}

	list, err := s.ListPodcasts(ctx)
	if err != nil || len(list) != 1 || list[0].EpisodeCount != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}

	// Baseline episodes are announced
	pending, err := s.ListPendingEpisodes(ctx, nil)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}

	// Cascade delete
	if err := s.DeletePodcast(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListPodcasts(ctx)
	if len(list) != 0 {
		t.Fatalf("expected empty, got %d", len(list))
	}
}

func TestAliasAndURLUniqueness(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://a.example/feed", ResolvedURL: "https://a.example/feed",
		Alias: strp("alpha"), Title: "A", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://b.example/feed", ResolvedURL: "https://b.example/feed",
		Alias: strp("ALPHA"), Title: "B", Now: now,
	})
	if err == nil {
		t.Fatal("expected alias unique conflict")
	}
	exists, err := s.AliasExists(ctx, "alpha", 0)
	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	_, ok, err := s.FindPodcastByURL(ctx, "https://a.example/feed")
	if err != nil || !ok {
		t.Fatalf("find url ok=%v err=%v", ok, err)
	}
}

func TestEpisodeUpsertAndAcknowledge(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	p, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://ex/feed", ResolvedURL: "https://ex/feed", Title: "T", Now: now,
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "guid:1", GUID: strp("1"), Title: "Old", EnclosureURL: "https://cdn/1.mp3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	n, err := s.UpsertEpisodes(ctx, p.ID, []store.BaselineEpisode{
		{IdentityKey: "guid:1", GUID: strp("1"), Title: "Old Updated", EnclosureURL: "https://cdn/1.mp3"},
		{IdentityKey: "guid:2", GUID: strp("2"), Title: "New", EnclosureURL: "https://cdn/2.mp3"},
	}, later)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	pending, err := s.ListPendingEpisodes(ctx, &p.ID)
	if err != nil || len(pending) != 1 || pending[0].Title != "New" {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if err := s.AcknowledgeEpisodes(ctx, []int64{pending[0].ID}, later); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.ListPendingEpisodes(ctx, &p.ID)
	if len(pending) != 0 {
		t.Fatalf("still pending: %v", pending)
	}

	// Metadata update should not re-pend
	n, err = s.UpsertEpisodes(ctx, p.ID, []store.BaselineEpisode{
		{IdentityKey: "guid:2", GUID: strp("2"), Title: "New Renamed", EnclosureURL: "https://cdn/2.mp3"},
	}, later.Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	ep, err := s.GetEpisode(ctx, pendingID(t, s, p.ID, "New Renamed"))
	if err != nil {
		// query by title via QueryEpisodes
		eps, err := s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, Query: "Renamed"})
		if err != nil || len(eps) != 1 {
			t.Fatalf("eps=%v err=%v", eps, err)
		}
		if eps[0].AnnouncedAt == nil {
			t.Fatal("renamed episode should stay announced")
		}
	} else if ep.AnnouncedAt == nil {
		t.Fatal("should stay announced")
	}
}

func pendingID(t *testing.T, s *store.Store, podcastID int64, title string) int64 {
	t.Helper()
	eps, err := s.QueryEpisodes(context.Background(), model.EpisodeFilter{All: true, PodcastID: &podcastID})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		if e.Title == title {
			return e.ID
		}
	}
	t.Fatalf("not found %s", title)
	return 0
}

func TestPlayedStateAndPlayCount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://ex/f", ResolvedURL: "https://ex/f", Title: "T", Now: now,
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "g:1", Title: "E", EnclosureURL: "https://cdn/e.mp3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, PodcastID: &p.ID})
	id := eps[0].ID

	ep, err := s.MarkPlayed(ctx, id, now)
	if err != nil || ep.PlayedAt == nil || ep.PlayCount != 0 {
		t.Fatalf("mark played: %#v err=%v", ep, err)
	}
	ep, err = s.MarkUnplayed(ctx, id)
	if err != nil || ep.PlayedAt != nil || ep.PlayCount != 0 {
		t.Fatalf("unplayed: %#v err=%v", ep, err)
	}
	ep, err = s.RecordPlayback(ctx, id, now)
	if err != nil || ep.PlayCount != 1 || ep.PlayedAt == nil {
		t.Fatalf("playback: %#v err=%v", ep, err)
	}
	ep, err = s.MarkUnplayed(ctx, id)
	if err != nil || ep.PlayCount != 1 || ep.PlayedAt != nil {
		t.Fatalf("manual unplayed keeps count: %#v", ep)
	}
}

func TestQueryFiltersAndLikeEscape(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p, _, _ := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://ex/f", ResolvedURL: "https://ex/f", Title: "T", Now: now,
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "1", Title: "100%_done", Description: strp("compiler talk"), EnclosureURL: "https://cdn/1.mp3"},
			{IdentityKey: "2", Title: "other", EnclosureURL: "https://cdn/2.mp3"},
		},
	})
	// mark one played
	eps, _ := s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, PodcastID: &p.ID})
	_, _ = s.MarkPlayed(ctx, eps[0].ID, now)

	got, err := s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, Query: "100%"})
	if err != nil || len(got) != 1 {
		t.Fatalf("literal percent: %v err=%v", got, err)
	}
	got, err = s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, Query: "compiler"})
	if err != nil || len(got) != 1 {
		t.Fatalf("desc search: %v err=%v", got, err)
	}
	tr := true
	got, err = s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, Played: &tr})
	if err != nil || len(got) != 1 {
		t.Fatalf("played filter: %d err=%v", len(got), err)
	}
}

func TestValidateAlias(t *testing.T) {
	if err := store.ValidateAlias("123"); err == nil {
		t.Fatal("digits")
	}
	if err := store.ValidateAlias(""); err == nil {
		t.Fatal("empty")
	}
	if err := store.ValidateAlias("daily"); err != nil {
		t.Fatal(err)
	}
}

func strp(s string) *string { return &s }
