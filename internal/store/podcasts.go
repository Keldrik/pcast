package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Keldrik/pcast/internal/model"
)

const podcastSelectCols = `
	p.id, p.feed_url, p.resolved_url, p.alias, p.title, p.author, p.description,
	p.etag, p.last_modified, p.last_attempt_at, p.last_success_at, p.last_http_status,
	p.last_error, p.created_at, p.updated_at`

func scanPodcast(scanner interface {
	Scan(dest ...any) error
}) (model.Podcast, error) {
	var p model.Podcast
	var alias, author, desc, etag, lastMod, lastAttempt, lastSuccess, lastErr sql.NullString
	var lastHTTP sql.NullInt64
	var created, updated string
	err := scanner.Scan(
		&p.ID, &p.FeedURL, &p.ResolvedURL, &alias, &p.Title, &author, &desc,
		&etag, &lastMod, &lastAttempt, &lastSuccess, &lastHTTP,
		&lastErr, &created, &updated,
	)
	if err != nil {
		return p, err
	}
	p.Alias = scanNullString(alias)
	p.Author = scanNullString(author)
	p.Description = scanNullString(desc)
	p.ETag = scanNullString(etag)
	p.LastModified = scanNullString(lastMod)
	p.LastError = scanNullString(lastErr)
	if lastHTTP.Valid {
		v := int(lastHTTP.Int64)
		p.LastHTTPStatus = &v
	}
	var err2 error
	if p.LastAttemptAt, err2 = scanTimePtr(lastAttempt); err2 != nil {
		return p, err2
	}
	if p.LastSuccessAt, err2 = scanTimePtr(lastSuccess); err2 != nil {
		return p, err2
	}
	if p.CreatedAt, err2 = parseTime(created); err2 != nil {
		return p, err2
	}
	if p.UpdatedAt, err2 = parseTime(updated); err2 != nil {
		return p, err2
	}
	return p, nil
}

// CreatePodcastInput holds fields for a new subscription.
type CreatePodcastInput struct {
	FeedURL     string
	ResolvedURL string
	Alias       *string
	Title       string
	Author      *string
	Description *string
	ETag        *string
	LastMod     *string
	HTTPStatus  *int
	Episodes    []BaselineEpisode
	Now         time.Time
}

// BaselineEpisode is inserted as already-announced on add.
type BaselineEpisode struct {
	IdentityKey     string
	GUID            *string
	Title           string
	Description     *string
	PublishedAt     *time.Time
	DurationSeconds *int
	EnclosureURL    string
	MediaType       *string
	MediaLength     *int64
}

// CreatePodcastWithBaseline inserts a podcast and baseline episodes in one transaction.
func (s *Store) CreatePodcastWithBaseline(ctx context.Context, in CreatePodcastInput) (model.Podcast, int, error) {
	var out model.Podcast
	var count int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		now := in.Now.UTC()
		// Reject feed/resolved collisions against either column of any existing row.
		// Scheme and host are normalized by the feed layer; path/query bytes remain
		// case-sensitive and must use binary comparison here.
		var conflict int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM podcasts
			WHERE feed_url = ? COLLATE BINARY OR resolved_url = ? COLLATE BINARY
			   OR feed_url = ? COLLATE BINARY OR resolved_url = ? COLLATE BINARY
			LIMIT 1`,
			in.FeedURL, in.FeedURL, in.ResolvedURL, in.ResolvedURL,
		).Scan(&conflict)
		if err == nil {
			return model.Storage("insert podcast: unique constraint", nil)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.Storage("check podcast url conflict", err)
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO podcasts (
				feed_url, resolved_url, alias, title, author, description,
				etag, last_modified, last_attempt_at, last_success_at, last_http_status,
				last_error, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
			in.FeedURL, in.ResolvedURL, nullString(in.Alias), in.Title,
			nullString(in.Author), nullString(in.Description),
			nullString(in.ETag), nullString(in.LastMod),
			formatTime(now), formatTime(now), nullInt(in.HTTPStatus),
			formatTime(now), formatTime(now),
		)
		if err != nil {
			return fmtErr("insert podcast", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return model.Storage("podcast id", err)
		}
		announced := formatTime(now)
		for _, ep := range dedupeBaselineEpisodes(in.Episodes) {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO episodes (
					podcast_id, identity_key, guid, title, description, published_at, published_at_ns,
					duration_seconds, enclosure_url, media_type, media_length,
					first_seen_at, updated_at, announced_at, played_at, play_count, last_played_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, NULL)`,
				id, ep.IdentityKey, nullString(ep.GUID), ep.Title, nullString(ep.Description),
				formatTimePtr(ep.PublishedAt), formatTimeNanosPtr(ep.PublishedAt), nullInt(ep.DurationSeconds), ep.EnclosureURL,
				nullString(ep.MediaType), nullInt64(ep.MediaLength),
				formatTime(now), formatTime(now), announced,
			)
			if err != nil {
				return fmtErr("insert baseline episode", err)
			}
			count++
		}
		row := tx.QueryRowContext(ctx, `SELECT `+podcastSelectCols+` FROM podcasts p WHERE p.id = ?`, id)
		out, err = scanPodcast(row)
		if err != nil {
			return model.Storage("reload podcast", err)
		}
		out.EpisodeCount = count
		out.UnplayedCount = count // baseline is unplayed
		return nil
	})
	return out, count, err
}

// GetPodcastByID returns a podcast by local ID with episode counts.
func (s *Store) GetPodcastByID(ctx context.Context, id int64) (model.Podcast, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+podcastSelectCols+podcastCountCols+`
		FROM podcasts p WHERE p.id = ?`, id)
	p, err := scanPodcastWithCounts(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, model.NotFoundf("no podcast with id %d", id)
	}
	if err != nil {
		return p, model.Storage("get podcast", err)
	}
	return p, nil
}

func scanPodcastWithCounts(scanner interface {
	Scan(dest ...any) error
}) (model.Podcast, error) {
	var p model.Podcast
	var alias, author, desc, etag, lastMod, lastAttempt, lastSuccess, lastErr sql.NullString
	var lastHTTP sql.NullInt64
	var created, updated string
	var epCount, unplayed int
	err := scanner.Scan(
		&p.ID, &p.FeedURL, &p.ResolvedURL, &alias, &p.Title, &author, &desc,
		&etag, &lastMod, &lastAttempt, &lastSuccess, &lastHTTP,
		&lastErr, &created, &updated, &epCount, &unplayed,
	)
	if err != nil {
		return p, err
	}
	p.Alias = scanNullString(alias)
	p.Author = scanNullString(author)
	p.Description = scanNullString(desc)
	p.ETag = scanNullString(etag)
	p.LastModified = scanNullString(lastMod)
	p.LastError = scanNullString(lastErr)
	if lastHTTP.Valid {
		v := int(lastHTTP.Int64)
		p.LastHTTPStatus = &v
	}
	var err2 error
	if p.LastAttemptAt, err2 = scanTimePtr(lastAttempt); err2 != nil {
		return p, err2
	}
	if p.LastSuccessAt, err2 = scanTimePtr(lastSuccess); err2 != nil {
		return p, err2
	}
	if p.CreatedAt, err2 = parseTime(created); err2 != nil {
		return p, err2
	}
	if p.UpdatedAt, err2 = parseTime(updated); err2 != nil {
		return p, err2
	}
	p.EpisodeCount = epCount
	p.UnplayedCount = unplayed
	return p, nil
}

const podcastCountCols = `,
			(SELECT COUNT(*) FROM episodes e WHERE e.podcast_id = p.id),
			(SELECT COUNT(*) FROM episodes e WHERE e.podcast_id = p.id AND e.played_at IS NULL)`

// FindPodcastByURL matches submitted or resolved feed URL (normalized comparison is caller's job).
func (s *Store) FindPodcastByURL(ctx context.Context, url string) (model.Podcast, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+podcastSelectCols+podcastCountCols+`
		FROM podcasts p
		WHERE p.feed_url = ? COLLATE BINARY OR p.resolved_url = ? COLLATE BINARY
		LIMIT 1`, url, url)
	p, err := scanPodcastWithCounts(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, model.Storage("find podcast by url", err)
	}
	return p, true, nil
}

// FindPodcastByAlias matches alias case-insensitively.
func (s *Store) FindPodcastByAlias(ctx context.Context, alias string) (model.Podcast, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+podcastSelectCols+podcastCountCols+`
		FROM podcasts p
		WHERE p.alias = ? COLLATE NOCASE
		LIMIT 1`, alias)
	p, err := scanPodcastWithCounts(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, model.Storage("find podcast by alias", err)
	}
	return p, true, nil
}

// FindPodcastsByTitle returns all exact case-insensitive title matches.
func (s *Store) FindPodcastsByTitle(ctx context.Context, title string) ([]model.Podcast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+podcastSelectCols+` FROM podcasts p
		WHERE p.title = ? COLLATE NOCASE
		ORDER BY p.id ASC`, title)
	if err != nil {
		return nil, model.Storage("find podcasts by title", err)
	}
	defer rows.Close()
	var out []model.Podcast
	for rows.Next() {
		p, err := scanPodcast(rows)
		if err != nil {
			return nil, model.Storage("scan podcast", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListPodcasts returns all podcasts ordered by ID with episode counts.
func (s *Store) ListPodcasts(ctx context.Context) ([]model.Podcast, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+podcastSelectCols+`,
			(SELECT COUNT(*) FROM episodes e WHERE e.podcast_id = p.id) AS episode_count,
			(SELECT COUNT(*) FROM episodes e WHERE e.podcast_id = p.id AND e.played_at IS NULL) AS unplayed_count
		FROM podcasts p
		ORDER BY p.id ASC`)
	if err != nil {
		return nil, model.Storage("list podcasts", err)
	}
	defer rows.Close()
	var out []model.Podcast
	for rows.Next() {
		var p model.Podcast
		var alias, author, desc, etag, lastMod, lastAttempt, lastSuccess, lastErr sql.NullString
		var lastHTTP sql.NullInt64
		var created, updated string
		var epCount, unplayed int
		err := rows.Scan(
			&p.ID, &p.FeedURL, &p.ResolvedURL, &alias, &p.Title, &author, &desc,
			&etag, &lastMod, &lastAttempt, &lastSuccess, &lastHTTP,
			&lastErr, &created, &updated, &epCount, &unplayed,
		)
		if err != nil {
			return nil, model.Storage("scan podcast list", err)
		}
		p.Alias = scanNullString(alias)
		p.Author = scanNullString(author)
		p.Description = scanNullString(desc)
		p.ETag = scanNullString(etag)
		p.LastModified = scanNullString(lastMod)
		p.LastError = scanNullString(lastErr)
		if lastHTTP.Valid {
			v := int(lastHTTP.Int64)
			p.LastHTTPStatus = &v
		}
		var err2 error
		if p.LastAttemptAt, err2 = scanTimePtr(lastAttempt); err2 != nil {
			return nil, model.Storage("parse time", err2)
		}
		if p.LastSuccessAt, err2 = scanTimePtr(lastSuccess); err2 != nil {
			return nil, model.Storage("parse time", err2)
		}
		if p.CreatedAt, err2 = parseTime(created); err2 != nil {
			return nil, model.Storage("parse time", err2)
		}
		if p.UpdatedAt, err2 = parseTime(updated); err2 != nil {
			return nil, model.Storage("parse time", err2)
		}
		p.EpisodeCount = epCount
		p.UnplayedCount = unplayed
		out = append(out, p)
	}
	if out == nil {
		out = []model.Podcast{}
	}
	return out, rows.Err()
}

// DeletePodcast removes a podcast and cascades episodes.
func (s *Store) DeletePodcast(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM podcasts WHERE id = ?`, id)
	if err != nil {
		return model.Storage("delete podcast", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return model.Storage("delete podcast rows", err)
	}
	if n == 0 {
		return model.NotFoundf("no podcast with id %d", id)
	}
	return nil
}

// AliasExists reports whether another podcast already uses the alias.
func (s *Store) AliasExists(ctx context.Context, alias string, excludeID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM podcasts
		WHERE alias = ? COLLATE NOCASE AND id != ?`, alias, excludeID).Scan(&n)
	if err != nil {
		return false, model.Storage("check alias", err)
	}
	return n > 0, nil
}

// EpisodeCount returns the number of episodes for a podcast.
func (s *Store) EpisodeCount(ctx context.Context, podcastID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE podcast_id = ?`, podcastID).Scan(&n)
	if err != nil {
		return 0, model.Storage("count episodes", err)
	}
	return n, nil
}

func dedupeBaselineEpisodes(items []BaselineEpisode) []BaselineEpisode {
	seenIdentity := make(map[string]struct{}, len(items))
	seenGUID := make(map[string]struct{}, len(items))
	seenEnclosure := make(map[string]struct{}, len(items))
	out := make([]BaselineEpisode, 0, len(items))
	for _, item := range items {
		duplicate := false
		if item.IdentityKey != "" {
			_, duplicate = seenIdentity[item.IdentityKey]
		}
		if !duplicate && item.GUID != nil && *item.GUID != "" {
			_, duplicate = seenGUID[*item.GUID]
		}
		if !duplicate && item.EnclosureURL != "" {
			_, duplicate = seenEnclosure[item.EnclosureURL]
		}
		if duplicate {
			continue
		}
		if item.IdentityKey != "" {
			seenIdentity[item.IdentityKey] = struct{}{}
		}
		if item.GUID != nil && *item.GUID != "" {
			seenGUID[*item.GUID] = struct{}{}
		}
		if item.EnclosureURL != "" {
			seenEnclosure[item.EnclosureURL] = struct{}{}
		}
		out = append(out, item)
	}
	return out
}

// ValidateAlias checks alias rules.
func ValidateAlias(alias string) error {
	if alias == "" || strings.TrimSpace(alias) == "" {
		return model.InvalidArgument("alias must not be empty")
	}
	if strings.TrimSpace(alias) != alias {
		return model.InvalidArgument("alias must not have leading or trailing whitespace")
	}
	onlyDigits := true
	for _, r := range alias {
		if r < '0' || r > '9' {
			onlyDigits = false
			break
		}
	}
	if onlyDigits {
		return model.InvalidArgument("alias must not consist only of digits")
	}
	return nil
}
