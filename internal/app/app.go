package app

import (
	"context"
	"time"

	"github.com/Keldrik/pcast/internal/model"
	"github.com/Keldrik/pcast/internal/store"
)

// Clock provides the current time (injectable for tests).
type Clock interface {
	Now() time.Time
}

// RealClock uses time.Now.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// FixedClock returns a constant time.
type FixedClock struct{ T time.Time }

func (c FixedClock) Now() time.Time { return c.T.UTC() }

// Locker acquires the application mutation lock.
type Locker interface {
	WithLock(ctx context.Context, fn func(context.Context) error) error
}

// FeedClient fetches remote feeds.
type FeedClient interface {
	Fetch(ctx context.Context, opts FetchOpts) (model.ParsedFeed, error)
}

// FetchOpts mirrors feed.FetchOptions without importing feed into interface consumers unnecessarily.
type FetchOpts struct {
	URL          string
	ETag         *string
	LastModified *string
}

// StoreOpener opens the local database for diagnostics when normal command
// initialization has not already opened it.
type StoreOpener func(context.Context, string) (StorePort, error)

// Player runs external playback.
type Player interface {
	Resolve(explicit string, extraArgs []string) (PlayerRef, error)
	Play(ctx context.Context, ref PlayerRef, enclosureURL string) (PlayOutcome, error)
}

// PlayerRef is a resolved player executable.
type PlayerRef struct {
	Path     string
	Args     []string
	IsOpener bool
	Name     string
}

// PlayOutcome is the result of invoking a player.
type PlayOutcome struct {
	Player     string
	ExitStatus int
	HandOff    bool
}

// StorePort is the persistence surface used by application services.
type StorePort interface {
	CreatePodcastWithBaseline(ctx context.Context, in store.CreatePodcastInput) (model.Podcast, int, error)
	GetPodcastByID(ctx context.Context, id int64) (model.Podcast, error)
	FindPodcastByURL(ctx context.Context, url string) (model.Podcast, bool, error)
	FindPodcastByAlias(ctx context.Context, alias string) (model.Podcast, bool, error)
	FindPodcastsByTitle(ctx context.Context, title string) ([]model.Podcast, error)
	ListPodcasts(ctx context.Context) ([]model.Podcast, error)
	DeletePodcast(ctx context.Context, id int64) error
	AliasExists(ctx context.Context, alias string, excludeID int64) (bool, error)
	EpisodeCount(ctx context.Context, podcastID int64) (int, error)
	ListPendingEpisodes(ctx context.Context, podcastID *int64) ([]model.Episode, error)
	AcknowledgeEpisodes(ctx context.Context, ids []int64, now time.Time) error
	GetEpisode(ctx context.Context, id int64) (model.Episode, error)
	QueryEpisodes(ctx context.Context, f model.EpisodeFilter) ([]model.Episode, error)
	MarkPlayed(ctx context.Context, id int64, now time.Time) (model.Episode, error)
	MarkUnplayed(ctx context.Context, id int64) (model.Episode, error)
	RecordPlayback(ctx context.Context, id int64, now time.Time) (model.Episode, error)
	ApplyCheck(ctx context.Context, podcastID int64, resolvedURL string, title string, author, desc, etag, lastMod *string, httpStatus int, items []store.BaselineEpisode, now time.Time) (int, error)
	ApplyNotModified(ctx context.Context, podcastID int64, httpStatus int, now time.Time) error
	ApplyCheckFailure(ctx context.Context, podcastID int64, httpStatus *int, errMsg string, now time.Time) error
	SchemaVersion(ctx context.Context) (int, error)
	ForeignKeysEnabled(ctx context.Context) (bool, error)
	Close() error
}

// App is the application service root.
type App struct {
	Store       StorePort
	Feeds       FeedClient
	Player      Player
	Lock        Locker
	Clock       Clock
	DataDir     string
	DBPath      string
	OpenStore   StoreOpener
	Concurrency int
	Version     model.VersionInfo
}

// now returns the current clock time.
func (a *App) now() time.Time {
	if a.Clock == nil {
		return time.Now().UTC()
	}
	return a.Clock.Now().UTC()
}

// concurrency returns the feed worker count.
func (a *App) concurrency() int {
	if a.Concurrency <= 0 {
		return 4
	}
	return a.Concurrency
}

// nopLocker is used when no lock is configured (should not happen in production).
type nopLocker struct{}

func (nopLocker) WithLock(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (a *App) locker() Locker {
	if a.Lock == nil {
		return nopLocker{}
	}
	return a.Lock
}
