-- name: GetEntryByGUID :one
SELECT id, hash FROM entries WHERE feed_id = ? AND guid = ?;

-- name: TombstoneExists :one
SELECT 1 FROM tombstones WHERE feed_id = ? AND guid = ?;

-- name: InsertEntry :one
-- content_text/summary_text are plain-text projections (tags/entities stripped) that
-- the FTS index reads, so searches match visible words, not markup. The store fills
-- them via core.PlainText from the already-sanitised content/summary.
INSERT INTO entries (user_id, feed_id, guid, url, title, author, content, summary,
  content_text, summary_text,
  published_at, status, starred, read_at, created_at, hash, extract_state, next_extract_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread', 0, NULL, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateEntryContent :exec
UPDATE entries SET title = ?, author = ?, content = ?, summary = ?,
  content_text = ?, summary_text = ?,
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

-- name: CountEntries :one
SELECT COUNT(*) FROM entries;

-- name: CountDueExtractions :one
-- Mirrors ListPendingExtractions's WHERE clause exactly (ignoring ORDER BY/LIMIT),
-- so it counts precisely the backlog ListPendingExtractions would dispatch.
SELECT COUNT(*) FROM entries WHERE extract_state = 'pending' AND next_extract_at <= ?;

-- name: SetEntryContent :exec
-- extract_state = 'pending' in the WHERE is a compare-and-swap guard against
-- entries.id reuse (entries keep a plain rowid, unlike feeds): a stale Scraper
-- goroutine finishing after its entry was deleted and the id recycled would
-- otherwise stamp the wrong article's content onto the new occupant. Guarding on
-- 'pending' makes that write a no-op whenever the recycled row isn't itself
-- awaiting extraction (the common case). The normal scrape path leaves the row
-- 'pending' until this write, so it is unaffected. (audit B7)
UPDATE entries SET content = ?, content_text = ?, extract_state = 'done', next_extract_at = NULL, extract_error = ''
WHERE id = ? AND extract_state = 'pending';

-- name: GetFeedFullContent :one
-- System-internal read (no user scoping): UpsertEntries calls this inside its
-- transaction so a newly-ingested entry's extract_state reflects the feed's CURRENT
-- fetch_full_content, not a poll-time snapshot that a concurrent toggle has since
-- changed (audit B7). Single-writer serialization makes the read consistent with
-- the inserts in the same tx.
SELECT fetch_full_content FROM feeds WHERE id = ?;

-- name: UpdateExtractState :exec
UPDATE entries SET extract_state = ?, extract_attempts = ?, next_extract_at = ?, extract_error = ? WHERE id = ?;

-- name: MarkFeedEntriesPending :exec
-- Re-queueing a previously-'failed' entry also resets extract_attempts to 0 so a
-- re-enabled full-content feed gets a fresh MaxAttempts budget rather than going
-- terminal after a single new failure (audit B10). extract_error is cleared too so
-- a stale reason doesn't linger on a now-pending entry.
UPDATE entries SET extract_state = 'pending', next_extract_at = ?, extract_attempts = 0, extract_error = ''
WHERE feed_id = ? AND extract_state IN ('none','failed');

-- name: CancelFeedExtractions :exec
-- Disabling full-content clears the feed's extraction artifacts: queued ('pending')
-- entries are un-queued, and terminally-'failed' ones reset to 'none' with the
-- failure reason cleared. Otherwise the reader keeps showing an extraction-failed
-- note for a feed the user has turned extraction off for (audit B10).
UPDATE entries SET extract_state = 'none', next_extract_at = NULL, extract_error = ''
WHERE feed_id = ? AND extract_state IN ('pending','failed');

-- name: WeeklyEntryCount :one
SELECT COUNT(*) FROM entries
WHERE feed_id = sqlc.arg(feed_id)
  AND (CASE WHEN published_at > 0 THEN published_at ELSE created_at END) >= sqlc.arg(window_start)
  AND (CASE WHEN published_at > 0 THEN published_at ELSE created_at END) <= sqlc.arg(window_end);
