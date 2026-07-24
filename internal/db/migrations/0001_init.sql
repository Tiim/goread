CREATE TABLE feeds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    feed_url TEXT NOT NULL UNIQUE,
    site_url TEXT NOT NULL DEFAULT '',
    favicon BLOB,
    refresh_ttl_seconds INTEGER NOT NULL DEFAULT 0,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    folder TEXT NOT NULL DEFAULT '',
    last_refresh_at TEXT,
    last_success_at TEXT,
    refresh_error TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE articles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id INTEGER NOT NULL REFERENCES feeds (id) ON DELETE CASCADE,
    guid TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    published_at TEXT,
    updated_at TEXT,
    link TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    content_text TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    read INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'present',
    content_hash TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_articles_feed_id ON articles (feed_id);
CREATE UNIQUE INDEX idx_articles_feed_guid ON articles (feed_id, guid) WHERE guid != '';
CREATE INDEX idx_articles_feed_link ON articles (feed_id, link);
CREATE INDEX idx_articles_feed_content_hash ON articles (feed_id, content_hash);
