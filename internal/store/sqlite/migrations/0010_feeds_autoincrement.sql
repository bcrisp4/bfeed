-- Rebuild feeds with an AUTOINCREMENT primary key so a deleted feed's id is never
-- reused. Without it (plain INTEGER PRIMARY KEY = rowid), SQLite recycles max(id)+1
-- after the top row is deleted; a 60s background subscribe/refresh goroutine still
-- holding the old id then writes the old feed's URL/metadata/entries onto whatever
-- new feed recycled that id (audit B7). AUTOINCREMENT keeps ids strictly monotonic,
-- so every stale write lands on a now-nonexistent row and no-ops.
--
-- Runs with NO TRANSACTION: the table rebuild must DROP the feeds table, and with
-- foreign_keys ON a DROP performs an implicit cascade that would wipe entries and
-- tombstones. PRAGMA foreign_keys is a no-op inside a transaction, so we disable it
-- for the rebuild and re-enable after. Ids are carried over explicitly, preserving
-- every feed's identity (and thus its entries' feed_id references).

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys=OFF;

CREATE TABLE feeds_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_url TEXT NOT NULL,
  site_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
  checked_at INTEGER,
  next_check_at INTEGER NOT NULL,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  fetch_full_content INTEGER NOT NULL DEFAULT 0 CHECK (fetch_full_content IN (0,1)),
  ttl_seconds INTEGER,
  user_title TEXT NOT NULL DEFAULT '',
  UNIQUE (user_id, feed_url)
) STRICT;

INSERT INTO feeds_new (
  id, user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at,
  updated_at, category_id, fetch_full_content, ttl_seconds, user_title
)
SELECT
  id, user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at,
  updated_at, category_id, fetch_full_content, ttl_seconds, user_title
FROM feeds;

DROP TABLE feeds;
ALTER TABLE feeds_new RENAME TO feeds;

CREATE INDEX idx_feeds_due      ON feeds(next_check_at) WHERE disabled = 0;
CREATE INDEX idx_feeds_user     ON feeds(user_id);
CREATE INDEX idx_feeds_category ON feeds(category_id);

PRAGMA foreign_key_check;
PRAGMA foreign_keys=ON;

-- +goose Down
PRAGMA foreign_keys=OFF;

CREATE TABLE feeds_new (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_url TEXT NOT NULL,
  site_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
  checked_at INTEGER,
  next_check_at INTEGER NOT NULL,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  fetch_full_content INTEGER NOT NULL DEFAULT 0 CHECK (fetch_full_content IN (0,1)),
  ttl_seconds INTEGER,
  user_title TEXT NOT NULL DEFAULT '',
  UNIQUE (user_id, feed_url)
) STRICT;

INSERT INTO feeds_new (
  id, user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at,
  updated_at, category_id, fetch_full_content, ttl_seconds, user_title
)
SELECT
  id, user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at,
  updated_at, category_id, fetch_full_content, ttl_seconds, user_title
FROM feeds;

DROP TABLE feeds;
ALTER TABLE feeds_new RENAME TO feeds;

CREATE INDEX idx_feeds_due      ON feeds(next_check_at) WHERE disabled = 0;
CREATE INDEX idx_feeds_user     ON feeds(user_id);
CREATE INDEX idx_feeds_category ON feeds(category_id);

PRAGMA foreign_key_check;
PRAGMA foreign_keys=ON;
