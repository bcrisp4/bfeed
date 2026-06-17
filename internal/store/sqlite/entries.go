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
	defer tx.Rollback()
	q := s.q.WithTx(tx)

	var inserted []*core.Entry
	for _, e := range entries {
		// Skip tombstoned (feed_id, guid): never resurrect deleted entries.
		if _, err := q.TombstoneExists(ctx, sqlc.TombstoneExistsParams{FeedID: int64(feedID), Guid: e.GUID}); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, mapErr(err)
		}
		existingID, err := q.GetEntryByGUID(ctx, sqlc.GetEntryByGUIDParams{FeedID: int64(feedID), Guid: e.GUID})
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
			cur, err := q.GetEntry(ctx, sqlc.GetEntryParams{ID: existingID, UserID: int64(e.UserID)})
			if err != nil {
				return nil, mapErr(err)
			}
			if cur.Hash != e.Hash {
				if err := q.UpdateEntryContent(ctx, sqlc.UpdateEntryContentParams{
					Title:       e.Title,
					Author:      e.Author,
					Content:     e.Content,
					Summary:     e.Summary,
					PublishedAt: toUnix(e.PublishedAt),
					Url:         e.URL,
					Hash:        e.Hash,
					ID:          existingID,
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
	where = append(where, "user_id = ?")
	args = append(args, int64(userID))
	if f.FeedID != nil {
		where = append(where, "feed_id = ?")
		args = append(args, int64(*f.FeedID))
	}
	if f.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*f.Status))
	}
	if f.Starred != nil {
		where = append(where, "starred = ?")
		args = append(args, b2i(*f.Starred))
	}
	if f.Cursor != nil {
		// Strictly older than the cursor (newest-first keyset).
		where = append(where, "(published_at < ? OR (published_at = ? AND id < ?))")
		args = append(args, toUnix(f.Cursor.PublishedAt), toUnix(f.Cursor.PublishedAt), int64(f.Cursor.ID))
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := fmt.Sprintf(
		`SELECT id, user_id, feed_id, guid, url, title, author, content, summary,
		        published_at, status, starred, read_at, created_at, hash
		 FROM entries WHERE %s ORDER BY published_at DESC, id DESC LIMIT ?`,
		strings.Join(where, " AND "))
	args = append(args, int64(limit+1)) // fetch one extra to detect next page

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	defer rows.Close()

	var out []*core.Entry
	for rows.Next() {
		// Scan into local typed variables to avoid sqlc null-type dependency.
		var (
			id, userIDVal, feedID    int64
			guid, url, title, author string
			content, summary         string
			publishedAt              int64
			status                   string
			starred                  int64
			readAt                   sql.NullInt64
			createdAt                int64
			hash                     string
		)
		if err := rows.Scan(&id, &userIDVal, &feedID, &guid, &url, &title, &author,
			&content, &summary, &publishedAt, &status, &starred, &readAt, &createdAt, &hash); err != nil {
			return nil, nil, mapErr(err)
		}
		e := &core.Entry{
			ID:          core.ID(id),
			UserID:      core.ID(userIDVal),
			FeedID:      core.ID(feedID),
			GUID:        guid,
			URL:         url,
			Title:       title,
			Author:      author,
			Content:     content,
			Summary:     summary,
			PublishedAt: fromUnix(publishedAt),
			Status:      core.EntryStatus(status),
			Starred:     starred != 0,
			ReadAt:      ptrUnix(readAt),
			CreatedAt:   fromUnix(createdAt),
			Hash:        hash,
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapErr(err)
	}
	var next *core.Cursor
	if len(out) > limit {
		last := out[limit-1]
		next = &core.Cursor{PublishedAt: last.PublishedAt, ID: last.ID}
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
	q := fmt.Sprintf(`UPDATE entries SET status = ?, read_at = ? WHERE user_id = ? AND id IN (%s)`, ph)
	all := append([]any{string(st), readAt, int64(userID)}, args...)
	_, err := s.db.ExecContext(ctx, q, all...)
	return mapErr(err)
}

func (s *Store) SetStarred(ctx context.Context, userID core.ID, ids []core.ID, starred bool) error {
	if len(ids) == 0 {
		return nil
	}
	ph, args := placeholders(ids)
	q := fmt.Sprintf(`UPDATE entries SET starred = ? WHERE user_id = ? AND id IN (%s)`, ph)
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
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	if err := q.InsertTombstone(ctx, sqlc.InsertTombstoneParams{
		FeedID:    int64(e.FeedID),
		Guid:      e.GUID,
		DeletedAt: time.Now().UTC().Unix(),
	}); err != nil {
		return mapErr(err)
	}
	if _, err := q.DeleteEntry(ctx, sqlc.DeleteEntryParams{ID: int64(entryID), UserID: int64(userID)}); err != nil {
		return mapErr(err)
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
