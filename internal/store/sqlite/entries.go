package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/store/sqlite/sqlc"
)

func (s *Store) UpsertEntries(ctx context.Context, feedID core.ID, entries []*core.Entry) ([]*core.Entry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)

	var inserted []*core.Entry
	for _, e := range entries {
		// Skip tombstoned (feed_id, guid): never resurrect deleted entries.
		if _, err := q.TombstoneExists(ctx, sqlc.TombstoneExistsParams{FeedID: int64(feedID), Guid: e.GUID}); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, mapErr(err)
		}
		existing, err := q.GetEntryByGUID(ctx, sqlc.GetEntryByGUIDParams{FeedID: int64(feedID), Guid: e.GUID})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id, err := q.InsertEntry(ctx, sqlc.InsertEntryParams{
				UserID:      int64(e.UserID),
				FeedID:      int64(feedID),
				Guid:        e.GUID,
				Url:         e.URL,
				Title:       e.Title,
				Author:      e.Author,
				Content:     e.Content,
				Summary:     e.Summary,
				PublishedAt: toUnix(e.PublishedAt),
				CreatedAt:   toUnix(e.CreatedAt),
				Hash:        e.Hash,
			})
			if err != nil {
				return nil, mapErr(err)
			}
			e.ID = core.ID(id)
			e.Status = core.StatusUnread
			inserted = append(inserted, e)
		case err != nil:
			return nil, mapErr(err)
		default:
			// Existing: update only if content hash changed (in-place edit).
			if existing.Hash != e.Hash {
				if err := q.UpdateEntryContent(ctx, sqlc.UpdateEntryContentParams{
					Title:       e.Title,
					Author:      e.Author,
					Content:     e.Content,
					Summary:     e.Summary,
					PublishedAt: toUnix(e.PublishedAt),
					Url:         e.URL,
					Hash:        e.Hash,
					ID:          existing.ID,
					UserID:      int64(e.UserID),
				}); err != nil {
					return nil, mapErr(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, mapErr(err)
	}
	return inserted, nil
}

func entryFromRow(r sqlc.Entry) *core.Entry {
	return &core.Entry{
		ID:          core.ID(r.ID),
		UserID:      core.ID(r.UserID),
		FeedID:      core.ID(r.FeedID),
		GUID:        r.Guid,
		URL:         r.Url,
		Title:       r.Title,
		Author:      r.Author,
		Content:     r.Content,
		Summary:     r.Summary,
		PublishedAt: fromUnix(r.PublishedAt),
		Status:      core.EntryStatus(r.Status),
		Starred:     r.Starred != 0,
		ReadAt:      ptrUnix(r.ReadAt),
		CreatedAt:   fromUnix(r.CreatedAt),
		Hash:        r.Hash,
	}
}

// scanEntry scans one row of the canonical 15-column entries projection
// (id, user_id, feed_id, guid, url, title, author, content, summary,
// published_at, status, starred, read_at, created_at, hash) into a core.Entry.
// Shared by ListEntries and Search so the column list lives in one place.
func scanEntry(rows *sql.Rows) (*core.Entry, error) {
	var r sqlc.Entry
	if err := rows.Scan(&r.ID, &r.UserID, &r.FeedID, &r.Guid, &r.Url, &r.Title, &r.Author,
		&r.Content, &r.Summary, &r.PublishedAt, &r.Status, &r.Starred, &r.ReadAt, &r.CreatedAt, &r.Hash); err != nil {
		return nil, err
	}
	return entryFromRow(r), nil
}

func (s *Store) GetEntry(ctx context.Context, userID, entryID core.ID) (*core.Entry, error) {
	r, err := s.q.GetEntry(ctx, sqlc.GetEntryParams{ID: int64(entryID), UserID: int64(userID)})
	if err != nil {
		return nil, mapErr(err)
	}
	return entryFromRow(r), nil
}

// ListEntries: dynamic WHERE + keyset on (published_at, id). Hand-written SQL
// because filters and the cursor predicate are conditional.
func (s *Store) ListEntries(ctx context.Context, userID core.ID, f core.EntryFilter) ([]*core.Entry, *core.Cursor, error) {
	var where []string
	var args []any
	where = append(where, "e.user_id = ?")
	args = append(args, int64(userID))
	if f.FeedID != nil {
		where = append(where, "e.feed_id = ?")
		args = append(args, int64(*f.FeedID))
	}
	if f.Status != nil {
		where = append(where, "e.status = ?")
		args = append(args, string(*f.Status))
	}
	if f.Starred != nil {
		where = append(where, "e.starred = ?")
		args = append(args, b2i(*f.Starred))
	}
	// Category filter joins feeds (entries don't carry category). The all-statuses
	// published-order scan is served sort-free by idx_entries_user_pub.
	join := ""
	if f.CategoryID != nil {
		join = " JOIN feeds f ON e.feed_id = f.id"
		where = append(where, "f.category_id = ?")
		args = append(args, int64(*f.CategoryID))
	} else if f.Uncategorised {
		join = " JOIN feeds f ON e.feed_id = f.id"
		where = append(where, "f.category_id IS NULL")
	}
	// Order column: history (read_at) vs default (published_at). The column name
	// comes from this closed switch, never from user input.
	orderCol := "e.published_at"
	if f.Order == core.OrderReadAtDesc {
		orderCol = "e.read_at"
		where = append(where, "e.read_at IS NOT NULL") // history membership
	}
	if f.Cursor != nil {
		// Strictly older than the cursor (newest-first keyset).
		where = append(where, fmt.Sprintf("(%s < ? OR (%s = ? AND e.id < ?))", orderCol, orderCol))
		args = append(args, f.Cursor.Key, f.Cursor.Key, int64(f.Cursor.ID))
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := fmt.Sprintf( //nolint:gosec // G201: orderCol is allowlisted; values are bound params
		`SELECT e.id, e.user_id, e.feed_id, e.guid, e.url, e.title, e.author, e.content, e.summary,
		        e.published_at, e.status, e.starred, e.read_at, e.created_at, e.hash
		 FROM entries e%s WHERE %s ORDER BY %s DESC, e.id DESC LIMIT ?`,
		join, strings.Join(where, " AND "), orderCol)
	args = append(args, int64(limit+1)) // fetch one extra to detect next page

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	defer rows.Close()

	var out []*core.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, nil, mapErr(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapErr(err)
	}
	var next *core.Cursor
	if len(out) > limit {
		last := out[limit-1]
		next = &core.Cursor{Key: sortKey(last, f.Order), ID: last.ID}
		out = out[:limit]
	}
	return out, next, nil
}

func (s *Store) SetStatus(ctx context.Context, userID core.ID, ids []core.ID, st core.EntryStatus) error {
	if len(ids) == 0 {
		return nil
	}
	var readAt any
	if st == core.StatusRead {
		readAt = time.Now().UTC().Unix()
	} else {
		readAt = nil
	}
	ph, args := placeholders(ids)
	q := fmt.Sprintf(`UPDATE entries SET status = ?, read_at = ? WHERE user_id = ? AND id IN (%s)`, ph) //nolint:gosec // G201: %s is a generated ?-placeholder list
	all := append([]any{string(st), readAt, int64(userID)}, args...)
	_, err := s.db.ExecContext(ctx, q, all...)
	return mapErr(err)
}

func (s *Store) SetStarred(ctx context.Context, userID core.ID, ids []core.ID, starred bool) error {
	if len(ids) == 0 {
		return nil
	}
	ph, args := placeholders(ids)
	q := fmt.Sprintf(`UPDATE entries SET starred = ? WHERE user_id = ? AND id IN (%s)`, ph) //nolint:gosec // G201: %s is a generated ?-placeholder list
	all := append([]any{b2i(starred), int64(userID)}, args...)
	_, err := s.db.ExecContext(ctx, q, all...)
	return mapErr(err)
}

func (s *Store) DeleteEntry(ctx context.Context, userID, entryID core.ID) error {
	e, err := s.GetEntry(ctx, userID, entryID) // ownership + need feed_id/guid for tombstone
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	if err := q.InsertTombstone(ctx, sqlc.InsertTombstoneParams{
		FeedID:    int64(e.FeedID),
		Guid:      e.GUID,
		DeletedAt: time.Now().UTC().Unix(),
	}); err != nil {
		return mapErr(err)
	}
	n, err := q.DeleteEntry(ctx, sqlc.DeleteEntryParams{ID: int64(entryID), UserID: int64(userID)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return core.ErrNotFound
	}
	return mapErr(tx.Commit())
}

func placeholders(ids []core.ID) (string, []any) {
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = "?"
		args[i] = int64(id)
	}
	return strings.Join(parts, ","), args
}

// sortKey returns the unix-seconds value of the entry's active order column.
func sortKey(e *core.Entry, ord core.Order) int64 {
	if ord == core.OrderReadAtDesc && e.ReadAt != nil {
		return e.ReadAt.Unix()
	}
	return e.PublishedAt.Unix()
}
