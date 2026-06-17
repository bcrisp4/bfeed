-- +goose Up
PRAGMA foreign_keys=OFF;

CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
) STRICT;
INSERT INTO users (id, username, created_at) VALUES (1, 'ben', 0);

CREATE TABLE feeds (
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
  UNIQUE (user_id, feed_url)
) STRICT;
CREATE INDEX idx_feeds_due  ON feeds(next_check_at) WHERE disabled = 0;
CREATE INDEX idx_feeds_user ON feeds(user_id);

CREATE TABLE entries (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  published_at INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'unread' CHECK (status IN ('unread','read')),
  starred INTEGER NOT NULL DEFAULT 0 CHECK (starred IN (0,1)),
  read_at INTEGER,
  created_at INTEGER NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  UNIQUE (feed_id, guid)
) STRICT;
CREATE INDEX idx_entries_user_status_pub ON entries(user_id, status, published_at DESC, id DESC);
CREATE INDEX idx_entries_feed_pub        ON entries(feed_id, published_at DESC);
CREATE INDEX idx_entries_starred         ON entries(user_id, published_at DESC) WHERE starred = 1;

CREATE TABLE tombstones (
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  deleted_at INTEGER NOT NULL,
  PRIMARY KEY (feed_id, guid)
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE tombstones;
DROP TABLE entries;
DROP TABLE feeds;
DROP TABLE users;
