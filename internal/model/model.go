package model

import (
	"time"
)

// SchemaVersion is the JSON envelope schema version.
const SchemaVersion = 1

// Podcast is a subscribed feed with stable local identity.
type Podcast struct {
	ID             int64      `json:"id"`
	FeedURL        string     `json:"feed_url"`
	ResolvedURL    string     `json:"resolved_url"`
	Alias          *string    `json:"alias"`
	Title          string     `json:"title"`
	Author         *string    `json:"author"`
	Description    *string    `json:"description"`
	ETag           *string    `json:"etag"`
	LastModified   *string    `json:"last_modified"`
	LastAttemptAt  *time.Time `json:"last_attempt_at"`
	LastSuccessAt  *time.Time `json:"last_success_at"`
	LastHTTPStatus *int       `json:"last_http_status"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	EpisodeCount   int        `json:"episode_count,omitempty"`
	UnplayedCount  int        `json:"unplayed_count,omitempty"`
}

// Episode is a playable feed item with stable local identity.
type Episode struct {
	ID              int64      `json:"id"`
	PodcastID       int64      `json:"podcast_id"`
	PodcastAlias    *string    `json:"podcast_alias,omitempty"`
	PodcastTitle    string     `json:"podcast_title,omitempty"`
	IdentityKey     string     `json:"identity_key"`
	GUID            *string    `json:"guid"`
	Title           string     `json:"title"`
	Description     *string    `json:"description"`
	PublishedAt     *time.Time `json:"published_at"`
	DurationSeconds *int       `json:"duration_seconds"`
	EnclosureURL    string     `json:"enclosure_url"`
	MediaType       *string    `json:"media_type"`
	MediaLength     *int64     `json:"media_length"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	AnnouncedAt     *time.Time `json:"announced_at"`
	PlayedAt        *time.Time `json:"played_at"`
	PlayCount       int        `json:"play_count"`
	LastPlayedAt    *time.Time `json:"last_played_at"`
}

// IsPlayed reports whether the episode has a played timestamp.
func (e Episode) IsPlayed() bool {
	return e.PlayedAt != nil
}

// IsPending reports whether the episode has not been announced by latest.
func (e Episode) IsPending() bool {
	return e.AnnouncedAt == nil
}

// ParsedFeed is the result of fetching and parsing a remote feed.
type ParsedFeed struct {
	SubmittedURL string
	ResolvedURL  string
	Title        string
	Author       *string
	Description  *string
	ETag         *string
	LastModified *string
	HTTPStatus   int
	NotModified  bool
	Episodes     []ParsedEpisode
}

// ParsedEpisode is one feed item with a usable enclosure.
type ParsedEpisode struct {
	GUID            *string
	Title           string
	Description     *string
	PublishedAt     *time.Time
	DurationSeconds *int
	EnclosureURL    string
	MediaType       *string
	MediaLength     *int64
	IdentityKey     string
}

// FeedCheck is the outcome of checking one podcast feed.
type FeedCheck struct {
	PodcastID  int64   `json:"podcast_id"`
	Status     string  `json:"status"` // ok | failed
	HTTPStatus *int    `json:"http_status,omitempty"`
	NewCount   int     `json:"new_count"`
	ErrorCode  *string `json:"error_code,omitempty"`
	Message    *string `json:"message,omitempty"`
}

// FeedFailure describes a failed feed check for partial latest results.
type FeedFailure struct {
	PodcastID int64  `json:"podcast_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// CheckStatus constants for FeedCheck.Status.
const (
	CheckStatusOK     = "ok"
	CheckStatusFailed = "failed"
)

// EpisodeFilter controls cached episode queries.
type EpisodeFilter struct {
	PodcastID *int64
	Limit     int
	Offset    int
	All       bool
	Played    *bool // nil = any, true = played only, false = unplayed only
	Pending   bool  // unannounced only
	Query     string
}

// Validate checks mutually exclusive filter options.
func (f EpisodeFilter) Validate() error {
	if f.All && (f.Limit != 0 || f.Offset != 0) {
		// Allow default limit zero when --all; reject explicit pagination with --all at CLI.
		if f.Limit > 0 || f.Offset > 0 {
			return InvalidArgument("--all is mutually exclusive with --limit and --offset")
		}
	}
	if f.Limit < 0 {
		return InvalidArgument("--limit must be >= 0")
	}
	if f.Offset < 0 {
		return InvalidArgument("--offset must be >= 0")
	}
	return nil
}

// EffectiveLimit returns the row limit, defaulting to 20 unless All is set.
func (f EpisodeFilter) EffectiveLimit() int {
	if f.All {
		return 0
	}
	if f.Limit == 0 {
		return 20
	}
	return f.Limit
}

// AddResult is returned by the add use case.
type AddResult struct {
	Podcast       Podcast `json:"podcast"`
	Created       bool    `json:"created"`
	EpisodeCount  int     `json:"episode_count"`
	ImportedCount int     `json:"imported_count"`
}

// RemoveResult is returned by the remove use case.
type RemoveResult struct {
	Podcast Podcast `json:"podcast"`
}

// ListResult is returned by the list use case.
type ListResult struct {
	Podcasts []Podcast `json:"podcasts"`
}

// LatestResult is returned by the latest use case before acknowledgement.
type LatestResult struct {
	Partial  bool          `json:"partial"`
	Checks   []FeedCheck   `json:"checks"`
	Episodes []Episode     `json:"episodes"`
	Failures []FeedFailure `json:"failures"`
}

// EpisodesResult is returned by the episodes query.
type EpisodesResult struct {
	Episodes []Episode `json:"episodes"`
}

// EpisodeResult is returned by episode detail.
type EpisodeResult struct {
	Episode Episode `json:"episode"`
}

// MarkResult is returned by mark.
type MarkResult struct {
	Episode Episode `json:"episode"`
}

// PlayResult is returned by play.
type PlayResult struct {
	Episode    Episode `json:"episode"`
	Player     string  `json:"player"`
	Marked     bool    `json:"marked_played"`
	ExitStatus int     `json:"exit_status"`
}

// DoctorCheck is one diagnostic check.
type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok | warn | error
	Message string `json:"message"`
}

// DoctorResult aggregates doctor checks.
type DoctorResult struct {
	DataDir string        `json:"data_dir"`
	Checks  []DoctorCheck `json:"checks"`
	OK      bool          `json:"ok"`
}

// VersionInfo holds build metadata.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// PlaybackOpts configures a play invocation.
type PlaybackOpts struct {
	Player       string
	PlayerArgs   []string
	NoMarkPlayed bool
}

// StringPtr returns a pointer to s, or nil when s is empty.
func StringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// IntPtr returns a pointer to n.
func IntPtr(n int) *int { return &n }

// Int64Ptr returns a pointer to n.
func Int64Ptr(n int64) *int64 { return &n }

// TimePtr returns a pointer to t, or nil when t is zero.
func TimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// DerefString returns the string or empty.
func DerefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
