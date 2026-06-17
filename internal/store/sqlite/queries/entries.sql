-- name: GetEntryByGUID :one
SELECT id, hash FROM entries WHERE feed_id = ? AND guid = ?;

-- name: TombstoneExists :one
SELECT 1 FROM tombstones WHERE feed_id = ? AND guid = ?;

-- name: InsertEntry :one
INSERT INTO entries (user_id, feed_id, guid, url, title, author, content, summary,
  published_at, status, starred, read_at, created_at, hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread', 0, NULL, ?, ?)
RETURNING id;

-- name: UpdateEntryContent :exec
UPDATE entries SET title = ?, author = ?, content = ?, summary = ?,
  published_at = ?, url = ?, hash = ? WHERE id = ? AND user_id = ?;

-- name: GetEntry :one
SELECT * FROM entries WHERE id = ? AND user_id = ?;

-- name: DeleteEntry :execrows
DELETE FROM entries WHERE id = ? AND user_id = ?;

-- name: InsertTombstone :exec
INSERT INTO tombstones (feed_id, guid, deleted_at) VALUES (?, ?, ?)
ON CONFLICT(feed_id, guid) DO UPDATE SET deleted_at = excluded.deleted_at;
