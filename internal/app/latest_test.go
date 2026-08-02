package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/platform"
	"github.com/Keldrik/pcast/internal/store"
)

type feedFunc func(context.Context, FetchOpts) (model.ParsedFeed, error)

func (f feedFunc) Fetch(ctx context.Context, opts FetchOpts) (model.ParsedFeed, error) {
	return f(ctx, opts)
}

type failingCheckStore struct {
	StorePort
	err error
}

func (s *failingCheckStore) ApplyCheck(context.Context, int64, string, string, *string, *string, *string, *string, int, []store.BaselineEpisode, time.Time) (int, error) {
	return 0, s.err
}

func newLatestTestStore(t *testing.T) (*store.Store, model.Podcast) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "pcast.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	p, _, err := s.CreatePodcastWithBaseline(context.Background(), store.CreatePodcastInput{
		FeedURL:     "https://example.test/feed.xml",
		ResolvedURL: "https://example.test/feed.xml",
		Title:       "Initial",
		Now:         time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestLatestStorageFailureIsFatal(t *testing.T) {
	s, p := newLatestTestStore(t)
	app := &App{
		Store: &failingCheckStore{StorePort: s, err: model.Storage("apply check", nil)},
		Feeds: feedFunc(func(context.Context, FetchOpts) (model.ParsedFeed, error) {
			return model.ParsedFeed{
				ResolvedURL: p.ResolvedURL,
				Title:       "Updated",
				HTTPStatus:  200,
			}, nil
		}),
		Clock: FixedClock{T: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
	called := false
	_, err := app.LatestLocked(context.Background(), "", func(model.LatestResult) error {
		called = true
		return nil
	})
	if model.CodeOf(err) != model.CodeStorageError {
		t.Fatalf("err=%v code=%s", err, model.CodeOf(err))
	}
	if called {
		t.Fatal("output ran after storage failure")
	}
}

func TestLatestTimeoutRecordsAttempt(t *testing.T) {
	s, _ := newLatestTestStore(t)
	when := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	app := &App{
		Store: s,
		Feeds: feedFunc(func(context.Context, FetchOpts) (model.ParsedFeed, error) {
			return model.ParsedFeed{}, model.FeedUnavailable("request failed", context.DeadlineExceeded)
		}),
		Clock: FixedClock{T: when},
	}
	result, err := app.LatestLocked(context.Background(), "", nil)
	if err != nil || !result.Partial {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	pods, err := s.ListPodcasts(context.Background())
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods=%v err=%v", pods, err)
	}
	if pods[0].LastAttemptAt == nil || !pods[0].LastAttemptAt.Equal(when) {
		t.Fatalf("last attempt=%v", pods[0].LastAttemptAt)
	}
	if pods[0].LastError == nil || *pods[0].LastError != "request timed out" {
		t.Fatalf("last error=%v", pods[0].LastError)
	}
}

type sequencedFeed struct {
	mu            sync.Mutex
	calls         int
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

func (f *sequencedFeed) Fetch(ctx context.Context, opts FetchOpts) (model.ParsedFeed, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	switch call {
	case 1:
		close(f.firstStarted)
		select {
		case <-f.releaseFirst:
		case <-ctx.Done():
			return model.ParsedFeed{}, ctx.Err()
		}
	case 2:
		close(f.secondStarted)
	}
	title := "Old"
	if call >= 2 {
		title = "New"
	}
	return model.ParsedFeed{
		ResolvedURL: opts.URL,
		Title:       title,
		HTTPStatus:  200,
		Episodes: []model.ParsedEpisode{{
			GUID:         model.StringPtr("same-guid"),
			Title:        title,
			EnclosureURL: "https://cdn.example.test/same.mp3",
			IdentityKey:  "guid:same-guid",
		}},
	}, nil
}

func TestLatestSerializesFetchAndApply(t *testing.T) {
	s, _ := newLatestTestStore(t)
	lockPath := filepath.Join(t.TempDir(), "pcast.lock")
	feed := &sequencedFeed{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
	makeApp := func() *App {
		return &App{
			Store: s,
			Feeds: feed,
			Lock:  platform.NewLock(lockPath),
			Clock: FixedClock{T: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		}
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := makeApp().LatestLocked(context.Background(), "", nil)
		firstDone <- err
	}()
	<-feed.firstStarted

	secondDone := make(chan error, 1)
	go func() {
		_, err := makeApp().LatestLocked(context.Background(), "", nil)
		secondDone <- err
	}()
	select {
	case <-feed.secondStarted:
		t.Fatal("second feed fetch started before first check released the lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(feed.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}

	pods, err := s.ListPodcasts(context.Background())
	if err != nil || len(pods) != 1 || pods[0].Title != "New" {
		t.Fatalf("pods=%v err=%v", pods, err)
	}
}
