-- Initial schema for pcast v1.

CREATE TABLE podcasts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_url TEXT NOT NULL COLLATE NOCASE,
    resolved_url TEXT NOT NULL COLLATE NOCASE,
    alias TEXT COLLATE NOCASE,
    title TEXT NOT NULL,
    author TEXT,
    description TEXT,
    etag TEXT,
    last_modified TEXT,
    last_attempt_at TEXT,
    last_success_at TEXT,
    last_http_status INTEGER,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_podcasts_feed_url ON podcasts(feed_url);
CREATE INDEX idx_podcasts_resolved_url ON podcasts(resolved_url);
CREATE UNIQUE INDEX idx_podcasts_alias ON podcasts(alias) WHERE alias IS NOT NULL;

CREATE TABLE episodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    podcast_id INTEGER NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
    identity_key TEXT NOT NULL,
    guid TEXT,
    title TEXT NOT NULL,
    description TEXT,
    published_at TEXT,
    duration_seconds INTEGER,
    enclosure_url TEXT NOT NULL,
    media_type TEXT,
    media_length INTEGER,
    first_seen_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    announced_at TEXT,
    played_at TEXT,
    play_count INTEGER NOT NULL DEFAULT 0,
    last_played_at TEXT
);

CREATE UNIQUE INDEX idx_episodes_podcast_identity ON episodes(podcast_id, identity_key);
CREATE INDEX idx_episodes_podcast_published ON episodes(podcast_id, published_at);
CREATE INDEX idx_episodes_announced_at ON episodes(announced_at);
CREATE INDEX idx_episodes_played_at ON episodes(played_at);
CREATE INDEX idx_episodes_guid ON episodes(podcast_id, guid);
CREATE INDEX idx_episodes_enclosure ON episodes(podcast_id, enclosure_url);
