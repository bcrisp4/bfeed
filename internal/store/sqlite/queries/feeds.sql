-- name: CreateFeed :one
INSERT INTO feeds (user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at, updated_at, category_id,
  fetch_full_content)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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

-- name: DeleteFeed :execrows
DELETE FROM feeds WHERE id = ? AND user_id = ?;

-- name: SetFeedCategory :execrows
UPDATE feeds SET category_id = ? WHERE id = ? AND user_id = ?;

-- name: SetFeedFullContent :execrows
UPDATE feeds SET fetch_full_content = ? WHERE id = ? AND user_id = ?;

-- name: EntryTotalsByFeed :many
SELECT feed_id, COUNT(*) AS n
FROM entries WHERE user_id = ? GROUP BY feed_id;

-- name: UnreadCountsByFeed :many
SELECT feed_id, COUNT(*) AS n
FROM entries WHERE user_id = ? AND status = 'unread' GROUP BY feed_id;
