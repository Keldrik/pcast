package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/store"
	_ "modernc.org/sqlite"
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
	if err != nil || v1 != 2 {
		t.Fatalf("v1=%d err=%v", v1, err)
	}
	_ = s1.Close()

	s2, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	v2, _ := s2.SchemaVersion(ctx)
	if v2 != 2 {
		t.Fatalf("v2=%d", v2)
	}
	fk, err := s2.ForeignKeysEnabled(ctx)
	if err != nil || !fk {
		t.Fatalf("fk=%v err=%v", fk, err)
	}
}

func TestOpenPathWithQuestionMark(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit '?' in file names")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "with?mark")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pcast.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "with")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected truncated database path, err=%v", err)
	}
}

func TestConcurrentOpenMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pcast.db")
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := store.Open(context.Background(), path)
			if err != nil {
				errs <- err
				return
			}
			errs <- s.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationKeepsLegacyDuplicateHints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pcast.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("migrations", "001_init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	const now = "2024-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO podcasts (feed_url, resolved_url, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"https://example.test/a", "https://cdn.test/feed", "A", now, now,
		"https://example.test/b", "https://cdn.test/feed", "B", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episodes (podcast_id, identity_key, guid, title, enclosure_url, first_seen_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)`,
		1, "identity-1", "same-guid", "One", "https://cdn.test/same.mp3", now, now,
		1, "identity-2", "same-guid", "Two", "https://cdn.test/same.mp3", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	version, err := s.SchemaVersion(context.Background())
	if err != nil || version != 2 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	pods, err := s.ListPodcasts(context.Background())
	if err != nil || len(pods) != 2 {
		t.Fatalf("podcasts=%v err=%v", pods, err)
	}
	eps, err := s.QueryEpisodes(context.Background(), model.EpisodeFilter{All: true})
	if err != nil || len(eps) != 2 {
		t.Fatalf("episodes=%v err=%v", eps, err)
	}
}

func TestURLPathCaseRemainsDistinct(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://example.test/Feed", ResolvedURL: "https://example.test/Feed", Title: "Upper", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://example.test/feed", ResolvedURL: "https://example.test/feed", Title: "Lower", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("case-sensitive URLs were merged")
	}
	got, ok, err := s.FindPodcastByURL(ctx, "https://example.test/feed")
	if err != nil || !ok || got.ID != second.ID {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestDuplicateBaselineItemsAreCollapsed(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	p, n, err := s.CreatePodcastWithBaseline(context.Background(), store.CreatePodcastInput{
		FeedURL: "https://example.test/feed", ResolvedURL: "https://example.test/feed", Title: "T", Now: now,
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "guid:same", GUID: strp("same"), Title: "First", EnclosureURL: "https://cdn.test/same.mp3"},
			{IdentityKey: "guid:other", GUID: strp("same"), Title: "Duplicate", EnclosureURL: "https://cdn.test/other.mp3"},
		},
	})
	if err != nil || n != 1 || p.EpisodeCount != 1 {
		t.Fatalf("podcast=%+v count=%d err=%v", p, n, err)
	}
}

func TestPublishedFractionalOrdering(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://example.test/time", ResolvedURL: "https://example.test/time", Title: "T", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertEpisodes(ctx, p.ID, []store.BaselineEpisode{
		{IdentityKey: "early", Title: "Early", EnclosureURL: "https://cdn.test/early.mp3", PublishedAt: timePtr(base.Add(100 * time.Nanosecond))},
		{IdentityKey: "late", Title: "Late", EnclosureURL: "https://cdn.test/late.mp3", PublishedAt: timePtr(base.Add(200 * time.Nanosecond))},
	}, now); err != nil {
		t.Fatal(err)
	}
	eps, err := s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, PodcastID: &p.ID})
	if err != nil || len(eps) != 2 || eps[0].Title != "Late" {
		t.Fatalf("episodes=%v err=%v", eps, err)
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

func TestIdentityKeyStableOnRematch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://ex/id", ResolvedURL: "https://ex/id", Title: "T", Now: now,
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "enc:https://cdn/x.mp3", Title: "Ep", EnclosureURL: "https://cdn/x.mp3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same enclosure, new guid-based identity — must not UNIQUE-collide by rewriting identity_key.
	n, err := s.UpsertEpisodes(ctx, p.ID, []store.BaselineEpisode{
		{IdentityKey: "guid:xyz", GUID: strp("xyz"), Title: "Ep", EnclosureURL: "https://cdn/x.mp3"},
	}, now.Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	eps, err := s.QueryEpisodes(ctx, model.EpisodeFilter{All: true, PodcastID: &p.ID})
	if err != nil || len(eps) != 1 {
		t.Fatalf("eps=%v err=%v", eps, err)
	}
	if eps[0].IdentityKey != "enc:https://cdn/x.mp3" {
		t.Fatalf("identity rewritten to %q", eps[0].IdentityKey)
	}
	if eps[0].GUID == nil || *eps[0].GUID != "xyz" {
		t.Fatalf("guid not updated: %v", eps[0].GUID)
	}
}

func TestResolvedURLUnique(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://a.example/1", ResolvedURL: "https://cdn.example/feed", Title: "A", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://b.example/2", ResolvedURL: "https://cdn.example/feed", Title: "B", Now: now,
	})
	if err == nil {
		t.Fatal("expected resolved_url unique conflict")
	}
}

func TestGetPodcastCounts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	created, n, err := s.CreatePodcastWithBaseline(ctx, store.CreatePodcastInput{
		FeedURL: "https://ex/counts", ResolvedURL: "https://ex/counts", Title: "T", Now: now,
		Episodes: []store.BaselineEpisode{
			{IdentityKey: "g:1", Title: "a", EnclosureURL: "https://cdn/a.mp3"},
			{IdentityKey: "g:2", Title: "b", EnclosureURL: "https://cdn/b.mp3"},
		},
	})
	if err != nil || n != 2 || created.EpisodeCount != 2 || created.UnplayedCount != 2 {
		t.Fatalf("created=%+v n=%d err=%v", created, n, err)
	}
	got, err := s.GetPodcastByID(ctx, created.ID)
	if err != nil || got.EpisodeCount != 2 || got.UnplayedCount != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
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

func strp(s string) *string          { return &s }
func timePtr(t time.Time) *time.Time { return &t }
