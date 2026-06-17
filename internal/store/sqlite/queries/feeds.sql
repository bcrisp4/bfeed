-- name: CreateFeed :one
INSERT INTO feeds (user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetFeed :one
SELECT * FROM feeds WHERE id = ? AND user_id = ?;

-- name: ListFeeds :many
SELECT * FROM feeds WHERE user_id = ? ORDER BY title COLLATE NOCASE ASC;

-- name: ListDueFeeds :many
SELECT * FROM feeds
WHERE disabled = 0 AND next_check_at <= ?
ORDER BY next_check_at ASC LIMIT ?;

-- name: UpdateFeed :exec
UPDATE feeds SET
  site_url = ?, title = ?, description = ?, etag = ?, last_modified = ?,
  disabled = ?, checked_at = ?, next_check_at = ?, error_count = ?, last_error = ?,
  updated_at = ?
WHERE id = ? AND user_id = ?;

-- name: DeleteFeedTombstones :exec
INSERT INTO tombstones (feed_id, guid, deleted_at)
SELECT entries.feed_id, entries.guid, ? FROM entries WHERE entries.feed_id = ?
ON CONFLICT(feed_id, guid) DO UPDATE SET deleted_at = excluded.deleted_at;

-- name: DeleteFeed :execrows
DELETE FROM feeds WHERE id = ? AND user_id = ?;
