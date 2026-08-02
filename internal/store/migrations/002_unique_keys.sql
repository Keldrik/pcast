-- Add nanosecond publication ordering without imposing new identity
-- uniqueness on imperfect feeds or existing v1 databases.
ALTER TABLE episodes ADD COLUMN published_at_ns INTEGER;

-- URL matching is case-sensitive after scheme/host normalization: path and
-- query bytes can be meaningful. Existing v1 indexes used NOCASE columns.
DROP INDEX IF EXISTS idx_podcasts_feed_url;
CREATE UNIQUE INDEX IF NOT EXISTS idx_podcasts_feed_url
    ON podcasts(feed_url COLLATE BINARY);

DROP INDEX IF EXISTS idx_podcasts_resolved_url;
CREATE INDEX IF NOT EXISTS idx_podcasts_resolved_url
    ON podcasts(resolved_url COLLATE BINARY);

-- GUIDs and enclosures are matching hints, not guaranteed unique feed data.
-- Keep these indexes non-unique so migration and imperfect feeds remain usable.
DROP INDEX IF EXISTS idx_episodes_podcast_guid;
CREATE INDEX IF NOT EXISTS idx_episodes_podcast_guid
    ON episodes(podcast_id, guid);

DROP INDEX IF EXISTS idx_episodes_podcast_enclosure;
CREATE INDEX IF NOT EXISTS idx_episodes_podcast_enclosure
    ON episodes(podcast_id, enclosure_url COLLATE BINARY);

CREATE INDEX IF NOT EXISTS idx_episodes_podcast_published_ns
    ON episodes(podcast_id, published_at_ns);
