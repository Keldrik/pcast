package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Keldrik/pcast/internal/model"
)

const episodeSelectCols = `
	e.id, e.podcast_id, e.identity_key, e.guid, e.title, e.description,
	e.published_at, e.duration_seconds, e.enclosure_url, e.media_type, e.media_length,
	e.first_seen_at, e.updated_at, e.announced_at, e.played_at, e.play_count, e.last_played_at`

func scanEpisode(scanner interface {
	Scan(dest ...any) error
}) (model.Episode, error) {
	var e model.Episode
	var guid, desc, published, mediaType, firstSeen, updated, announced, played, lastPlayed sql.NullString
	var dur, mediaLen sql.NullInt64
	err := scanner.Scan(
		&e.ID, &e.PodcastID, &e.IdentityKey, &guid, &e.Title, &desc,
		&published, &dur, &e.EnclosureURL, &mediaType, &mediaLen,
		&firstSeen, &updated, &announced, &played, &e.PlayCount, &lastPlayed,
	)
	if err != nil {
		return e, err
	}
	e.GUID = scanNullString(guid)
	e.Description = scanNullString(desc)
	e.MediaType = scanNullString(mediaType)
	e.DurationSeconds = scanNullInt(dur)
	e.MediaLength = scanNullInt64(mediaLen)
	var err2 error
	if e.PublishedAt, err2 = scanTimePtr(published); err2 != nil {
		return e, err2
	}
	if fs, err2 := scanTimePtr(firstSeen); err2 != nil {
		return e, err2
	} else if fs != nil {
		e.FirstSeenAt = *fs
	}
	if u, err2 := scanTimePtr(updated); err2 != nil {
		return e, err2
	} else if u != nil {
		e.UpdatedAt = *u
	}
	if e.AnnouncedAt, err2 = scanTimePtr(announced); err2 != nil {
		return e, err2
	}
	if e.PlayedAt, err2 = scanTimePtr(played); err2 != nil {
		return e, err2
	}
	if e.LastPlayedAt, err2 = scanTimePtr(lastPlayed); err2 != nil {
		return e, err2
	}
	return e, nil
}

// UpsertEpisodeResult describes one upsert outcome.
type UpsertEpisodeResult struct {
	Episode model.Episode
	Created bool
}

// UpsertEpisodes inserts new pending episodes and updates metadata for known ones.
// New episodes have announced_at NULL. Existing rows are never deleted.
func (s *Store) UpsertEpisodes(ctx context.Context, podcastID int64, items []BaselineEpisode, now time.Time) (created int, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		n, err := upsertEpisodesTx(ctx, tx, podcastID, items, now)
		created = n
		return err
	})
	return created, err
}

func upsertEpisodesTx(ctx context.Context, tx *sql.Tx, podcastID int64, items []BaselineEpisode, now time.Time) (int, error) {
	nowS := formatTime(now.UTC())
	created := 0
	for _, item := range items {
		existing, found, err := findEpisodeMatch(ctx, tx, podcastID, item)
		if err != nil {
			return created, err
		}
		if found {
			if err := updateEpisodeMeta(ctx, tx, existing.ID, item, nowS); err != nil {
				return created, err
			}
			continue
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO episodes (
				podcast_id, identity_key, guid, title, description, published_at,
				duration_seconds, enclosure_url, media_type, media_length,
				first_seen_at, updated_at, announced_at, played_at, play_count, last_played_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, 0, NULL)`,
			podcastID, item.IdentityKey, nullString(item.GUID), item.Title, nullString(item.Description),
			formatTimePtr(item.PublishedAt), nullInt(item.DurationSeconds), item.EnclosureURL,
			nullString(item.MediaType), nullInt64(item.MediaLength),
			nowS, nowS,
		)
		if err != nil {
			return created, fmtErr("insert episode", err)
		}
		created++
	}
	return created, nil
}

func findEpisodeMatch(ctx context.Context, tx *sql.Tx, podcastID int64, item BaselineEpisode) (model.Episode, bool, error) {
	// 1. identity_key
	row := tx.QueryRowContext(ctx, `
		SELECT `+episodeSelectCols+` FROM episodes e
		WHERE e.podcast_id = ? AND e.identity_key = ?`, podcastID, item.IdentityKey)
	ep, err := scanEpisode(row)
	if err == nil {
		return ep, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ep, false, model.Storage("match identity", err)
	}
	// 2. GUID
	if item.GUID != nil && *item.GUID != "" {
		row = tx.QueryRowContext(ctx, `
			SELECT `+episodeSelectCols+` FROM episodes e
			WHERE e.podcast_id = ? AND e.guid = ?`, podcastID, *item.GUID)
		ep, err = scanEpisode(row)
		if err == nil {
			return ep, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return ep, false, model.Storage("match guid", err)
		}
	}
	// 3. enclosure URL
	row = tx.QueryRowContext(ctx, `
		SELECT `+episodeSelectCols+` FROM episodes e
		WHERE e.podcast_id = ? AND e.enclosure_url = ?`, podcastID, item.EnclosureURL)
	ep, err = scanEpisode(row)
	if err == nil {
		return ep, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ep, false, model.Storage("match enclosure", err)
	}
	return model.Episode{}, false, nil
}

func updateEpisodeMeta(ctx context.Context, tx *sql.Tx, id int64, item BaselineEpisode, nowS string) error {
	// Preserve useful stored values when feed omits fields (COALESCE with existing via CASE).
	_, err := tx.ExecContext(ctx, `
		UPDATE episodes SET
			identity_key = ?,
			guid = COALESCE(?, guid),
			title = CASE WHEN ? != '' THEN ? ELSE title END,
			description = COALESCE(?, description),
			published_at = COALESCE(?, published_at),
			duration_seconds = COALESCE(?, duration_seconds),
			enclosure_url = CASE WHEN ? != '' THEN ? ELSE enclosure_url END,
			media_type = COALESCE(?, media_type),
			media_length = COALESCE(?, media_length),
			updated_at = ?
		WHERE id = ?`,
		item.IdentityKey,
		nullString(item.GUID),
		item.Title, item.Title,
		nullString(item.Description),
		formatTimePtr(item.PublishedAt),
		nullInt(item.DurationSeconds),
		item.EnclosureURL, item.EnclosureURL,
		nullString(item.MediaType),
		nullInt64(item.MediaLength),
		nowS, id,
	)
	return fmtErr("update episode meta", err)
}

// ListPendingEpisodes returns unannounced episodes, optionally scoped to a podcast.
func (s *Store) ListPendingEpisodes(ctx context.Context, podcastID *int64) ([]model.Episode, error) {
	q := `
		SELECT ` + episodeSelectCols + `, p.alias, p.title
		FROM episodes e
		JOIN podcasts p ON p.id = e.podcast_id
		WHERE e.announced_at IS NULL`
	args := []any{}
	if podcastID != nil {
		q += ` AND e.podcast_id = ?`
		args = append(args, *podcastID)
	}
	q += ` ORDER BY e.podcast_id ASC, e.published_at DESC, e.id ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, model.Storage("list pending", err)
	}
	defer rows.Close()
	return scanEpisodesWithPodcast(rows)
}

// AcknowledgeEpisodes sets announced_at for the given episode IDs.
func (s *Store) AcknowledgeEpisodes(ctx context.Context, ids []int64, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		nowS := formatTime(now.UTC())
		for _, id := range ids {
			_, err := tx.ExecContext(ctx, `
				UPDATE episodes SET announced_at = ? WHERE id = ? AND announced_at IS NULL`,
				nowS, id)
			if err != nil {
				return fmtErr("acknowledge episode", err)
			}
		}
		return nil
	})
}

// GetEpisode returns one episode by ID with podcast labels.
func (s *Store) GetEpisode(ctx context.Context, id int64) (model.Episode, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+episodeSelectCols+`, p.alias, p.title
		FROM episodes e
		JOIN podcasts p ON p.id = e.podcast_id
		WHERE e.id = ?`, id)
	ep, err := scanEpisodeWithPodcast(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ep, model.NotFoundf("no episode with id %d", id)
	}
	if err != nil {
		return ep, model.Storage("get episode", err)
	}
	return ep, nil
}

// QueryEpisodes returns filtered catalog rows.
func (s *Store) QueryEpisodes(ctx context.Context, f model.EpisodeFilter) ([]model.Episode, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(`SELECT ` + episodeSelectCols + `, p.alias, p.title FROM episodes e JOIN podcasts p ON p.id = e.podcast_id WHERE 1=1`)
	args := []any{}
	if f.PodcastID != nil {
		b.WriteString(` AND e.podcast_id = ?`)
		args = append(args, *f.PodcastID)
	}
	if f.Played != nil {
		if *f.Played {
			b.WriteString(` AND e.played_at IS NOT NULL`)
		} else {
			b.WriteString(` AND e.played_at IS NULL`)
		}
	}
	if f.Pending {
		b.WriteString(` AND e.announced_at IS NULL`)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		// Escape LIKE wildcards so user query is literal substring match.
		like := "%" + escapeLike(q) + "%"
		b.WriteString(` AND (e.title LIKE ? ESCAPE '\' COLLATE NOCASE OR IFNULL(e.description,'') LIKE ? ESCAPE '\' COLLATE NOCASE)`)
		args = append(args, like, like)
	}
	// Stable catalog order: published DESC NULLS LAST, id DESC.
	// SQLite sorts NULL as smallest by default for DESC we need NULLS LAST emulation.
	b.WriteString(` ORDER BY (e.published_at IS NULL) ASC, e.published_at DESC, e.id DESC`)
	limit := f.EffectiveLimit()
	if limit > 0 {
		b.WriteString(` LIMIT ?`)
		args = append(args, limit)
		if f.Offset > 0 {
			b.WriteString(` OFFSET ?`)
			args = append(args, f.Offset)
		}
	} else if f.Offset > 0 {
		b.WriteString(` LIMIT -1 OFFSET ?`)
		args = append(args, f.Offset)
	}
	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, model.Storage("query episodes", err)
	}
	defer rows.Close()
	return scanEpisodesWithPodcast(rows)
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func scanEpisodeWithPodcast(scanner interface {
	Scan(dest ...any) error
}) (model.Episode, error) {
	var e model.Episode
	var guid, desc, published, mediaType, firstSeen, updated, announced, played, lastPlayed sql.NullString
	var dur, mediaLen sql.NullInt64
	var alias sql.NullString
	var ptitle string
	err := scanner.Scan(
		&e.ID, &e.PodcastID, &e.IdentityKey, &guid, &e.Title, &desc,
		&published, &dur, &e.EnclosureURL, &mediaType, &mediaLen,
		&firstSeen, &updated, &announced, &played, &e.PlayCount, &lastPlayed,
		&alias, &ptitle,
	)
	if err != nil {
		return e, err
	}
	e.GUID = scanNullString(guid)
	e.Description = scanNullString(desc)
	e.MediaType = scanNullString(mediaType)
	e.DurationSeconds = scanNullInt(dur)
	e.MediaLength = scanNullInt64(mediaLen)
	e.PodcastAlias = scanNullString(alias)
	e.PodcastTitle = ptitle
	var err2 error
	if e.PublishedAt, err2 = scanTimePtr(published); err2 != nil {
		return e, err2
	}
	if fs, err2 := scanTimePtr(firstSeen); err2 != nil {
		return e, err2
	} else if fs != nil {
		e.FirstSeenAt = *fs
	}
	if u, err2 := scanTimePtr(updated); err2 != nil {
		return e, err2
	} else if u != nil {
		e.UpdatedAt = *u
	}
	if e.AnnouncedAt, err2 = scanTimePtr(announced); err2 != nil {
		return e, err2
	}
	if e.PlayedAt, err2 = scanTimePtr(played); err2 != nil {
		return e, err2
	}
	if e.LastPlayedAt, err2 = scanTimePtr(lastPlayed); err2 != nil {
		return e, err2
	}
	return e, nil
}

func scanEpisodesWithPodcast(rows *sql.Rows) ([]model.Episode, error) {
	var out []model.Episode
	for rows.Next() {
		ep, err := scanEpisodeWithPodcast(rows)
		if err != nil {
			return nil, model.Storage("scan episode", err)
		}
		out = append(out, ep)
	}
	if out == nil {
		out = []model.Episode{}
	}
	return out, rows.Err()
}

// MarkPlayed sets played_at without changing play_count.
func (s *Store) MarkPlayed(ctx context.Context, id int64, now time.Time) (model.Episode, error) {
	nowS := formatTime(now.UTC())
	res, err := s.db.ExecContext(ctx, `
		UPDATE episodes SET played_at = COALESCE(played_at, ?) WHERE id = ?`, nowS, id)
	if err != nil {
		return model.Episode{}, model.Storage("mark played", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Could be already played or missing; distinguish.
		if _, err := s.GetEpisode(ctx, id); err != nil {
			return model.Episode{}, err
		}
	}
	return s.GetEpisode(ctx, id)
}

// MarkUnplayed clears played_at without changing play_count.
func (s *Store) MarkUnplayed(ctx context.Context, id int64) (model.Episode, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE episodes SET played_at = NULL WHERE id = ?`, id)
	if err != nil {
		return model.Episode{}, model.Storage("mark unplayed", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if _, err := s.GetEpisode(ctx, id); err != nil {
			return model.Episode{}, err
		}
	}
	return s.GetEpisode(ctx, id)
}

// RecordPlayback increments play_count and sets played timestamps after successful play.
func (s *Store) RecordPlayback(ctx context.Context, id int64, now time.Time) (model.Episode, error) {
	nowS := formatTime(now.UTC())
	res, err := s.db.ExecContext(ctx, `
		UPDATE episodes SET
			played_at = COALESCE(played_at, ?),
			last_played_at = ?,
			play_count = play_count + 1
		WHERE id = ?`, nowS, nowS, id)
	if err != nil {
		return model.Episode{}, model.Storage("record playback", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.Episode{}, model.NotFoundf("no episode with id %d", id)
	}
	return s.GetEpisode(ctx, id)
}

// ApplyCheck applies a successful feed check: metadata, validators, and episode upserts.
func (s *Store) ApplyCheck(ctx context.Context, podcastID int64, resolvedURL string, title string, author, desc, etag, lastMod *string, httpStatus int, items []BaselineEpisode, now time.Time) (newCount int, err error) {
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		nowS := formatTime(now.UTC())
		_, err := tx.ExecContext(ctx, `
			UPDATE podcasts SET
				last_attempt_at = ?,
				last_success_at = ?,
				last_http_status = ?,
				etag = COALESCE(?, etag),
				last_modified = COALESCE(?, last_modified),
				last_error = NULL,
				title = CASE WHEN ? != '' THEN ? ELSE title END,
				author = COALESCE(?, author),
				description = COALESCE(?, description),
				resolved_url = CASE WHEN ? != '' THEN ? ELSE resolved_url END,
				updated_at = ?
			WHERE id = ?`,
			nowS, nowS, httpStatus,
			nullString(etag), nullString(lastMod),
			title, title,
			nullString(author), nullString(desc),
			resolvedURL, resolvedURL,
			nowS, podcastID,
		)
		if err != nil {
			return fmtErr("apply check podcast", err)
		}
		n, err := upsertEpisodesTx(ctx, tx, podcastID, items, now)
		newCount = n
		return err
	})
	return newCount, err
}

// ApplyNotModified records a 304 successful check.
func (s *Store) ApplyNotModified(ctx context.Context, podcastID int64, httpStatus int, now time.Time) error {
	nowS := formatTime(now.UTC())
	_, err := s.db.ExecContext(ctx, `
		UPDATE podcasts SET
			last_attempt_at = ?,
			last_success_at = ?,
			last_http_status = ?,
			last_error = NULL,
			updated_at = ?
		WHERE id = ?`, nowS, nowS, httpStatus, nowS, podcastID)
	return fmtErr("apply not modified", err)
}

// ApplyCheckFailure records a failed fetch without clearing success validators.
func (s *Store) ApplyCheckFailure(ctx context.Context, podcastID int64, httpStatus *int, errMsg string, now time.Time) error {
	nowS := formatTime(now.UTC())
	_, err := s.db.ExecContext(ctx, `
		UPDATE podcasts SET
			last_attempt_at = ?,
			last_http_status = ?,
			last_error = ?,
			updated_at = ?
		WHERE id = ?`, nowS, nullInt(httpStatus), errMsg, nowS, podcastID)
	return fmtErr("apply check failure", err)
}

// Debug helper suppress unused.
var _ = fmt.Sprintf
