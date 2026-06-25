-- name: GetEntryByGUID :one
SELECT id, hash FROM entries WHERE feed_id = ? AND guid = ?;

-- name: TombstoneExists :one
SELECT 1 FROM tombstones WHERE feed_id = ? AND guid = ?;

-- name: InsertEntry :one
INSERT INTO entries (user_id, feed_id, guid, url, title, author, content, summary,
  published_at, status, starred, read_at, created_at, hash, extract_state, next_extract_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread', 0, NULL, ?, ?, ?, ?)
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

-- name: ListPendingExtractions :many
SELECT * FROM entries
WHERE extract_state = 'pending' AND next_extract_at <= ?
ORDER BY published_at DESC, id DESC LIMIT ?;

-- name: SetEntryContent :exec
UPDATE entries SET content = ?, extract_state = 'done', next_extract_at = NULL WHERE id = ?;

-- name: UpdateExtractState :exec
UPDATE entries SET extract_state = ?, extract_attempts = ?, next_extract_at = ? WHERE id = ?;

-- name: MarkFeedEntriesPending :exec
UPDATE entries SET extract_state = 'pending', next_extract_at = ?
WHERE feed_id = ? AND extract_state IN ('none','failed');

-- name: CancelFeedExtractions :exec
UPDATE entries SET extract_state = 'none', next_extract_at = NULL
WHERE feed_id = ? AND extract_state = 'pending';

-- name: WeeklyEntryCount :one
SELECT COUNT(*) FROM entries
WHERE feed_id = sqlc.arg(feed_id)
  AND (CASE WHEN published_at > 0 THEN published_at ELSE created_at END) >= sqlc.arg(window_start)
  AND (CASE WHEN published_at > 0 THEN published_at ELSE created_at END) <= sqlc.arg(window_end);
