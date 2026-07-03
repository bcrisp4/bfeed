-- name: CreateFeed :one
INSERT INTO feeds (user_id, feed_url, site_url, title, description, etag, last_modified,
  disabled, checked_at, next_check_at, error_count, last_error, created_at, updated_at, category_id,
  fetch_full_content, ttl_seconds)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
-- feed_url in the WHERE is a compare-and-swap guard: a poll operates on a *Feed
-- snapshot captured at dispatch, so if the user edits the feed's URL mid-poll the
-- row's feed_url no longer matches and this blind full-row write becomes a no-op,
-- instead of resurrecting the old URL's etag/last_modified/metadata that SetFeedURL
-- deliberately cleared (audit B7). recordSuccess adopts a permanent-redirect URL via
-- SetFeedURL before this runs, so the guard uses the poll's current f.FeedURL.
UPDATE feeds SET
  site_url = ?, title = ?, description = ?, etag = ?, last_modified = ?,
  disabled = ?, checked_at = ?, next_check_at = ?, error_count = ?, last_error = ?,
  updated_at = ?, ttl_seconds = ?
WHERE id = ? AND user_id = ? AND feed_url = ?;

-- name: DeleteFeed :execrows
DELETE FROM feeds WHERE id = ? AND user_id = ?;

-- name: SetFeedCategory :execrows
UPDATE feeds SET category_id = ? WHERE id = ? AND user_id = ?;

-- name: SetFeedFullContent :execrows
UPDATE feeds SET fetch_full_content = ? WHERE id = ? AND user_id = ?;

-- name: EntryStatsByFeed :many
SELECT feed_id,
  COUNT(*)                                  AS total,
  COUNT(*) FILTER (WHERE status = 'unread') AS unread
FROM entries WHERE user_id = ? GROUP BY feed_id;

-- name: SetFeedUserTitle :execrows
UPDATE feeds SET user_title = ? WHERE id = ? AND user_id = ?;

-- name: SetFeedURL :execrows
-- Clears etag/last_modified too: they belong to the old URL, so reusing them as
-- conditional-GET headers against the new URL risks a spurious 304 that skips
-- the new feed's content. The next poll re-fetches in full and repopulates them.
-- Also moves next_check_at to now so the Poller re-fetches the new URL promptly:
-- a URL edit's startRefresh silently no-ops when a refresh of the OLD URL is still
-- in flight, and without this the new URL would wait for the old poll's far-future
-- adaptive next_check_at (audit B7). Poll-path callers (redirect adoption, discovery)
-- overwrite next_check_at via recordSuccess immediately after, so this is harmless there.
UPDATE feeds SET feed_url = ?, etag = '', last_modified = '', next_check_at = ? WHERE id = ? AND user_id = ?;
