package sqlite

import (
	"context"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/store/sqlite/sqlc"
)

func feedFromRow(r sqlc.Feed) *core.Feed {
	return &core.Feed{
		ID:           core.ID(r.ID),
		UserID:       core.ID(r.UserID),
		FeedURL:      r.FeedUrl,
		SiteURL:      r.SiteUrl,
		Title:        r.Title,
		Description:  r.Description,
		ETag:         r.Etag,
		LastModified: r.LastModified,
		Disabled:     r.Disabled != 0,
		CheckedAt:    ptrUnix(r.CheckedAt),
		NextCheckAt:  fromUnix(r.NextCheckAt),
		ErrorCount:   int(r.ErrorCount),
		LastError:    r.LastError,
		CreatedAt:    fromUnix(r.CreatedAt),
		UpdatedAt:    fromUnix(r.UpdatedAt),
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (s *Store) CreateFeed(ctx context.Context, f *core.Feed) (core.ID, error) {
	id, err := s.q.CreateFeed(ctx, sqlc.CreateFeedParams{
		UserID:       int64(f.UserID),
		FeedUrl:      f.FeedURL,
		SiteUrl:      f.SiteURL,
		Title:        f.Title,
		Description:  f.Description,
		Etag:         f.ETag,
		LastModified: f.LastModified,
		Disabled:     b2i(f.Disabled),
		CheckedAt:    nullUnix(f.CheckedAt),
		NextCheckAt:  toUnix(f.NextCheckAt),
		ErrorCount:   int64(f.ErrorCount),
		LastError:    f.LastError,
		CreatedAt:    toUnix(f.CreatedAt),
		UpdatedAt:    toUnix(f.UpdatedAt),
	})
	if err != nil {
		return 0, mapErr(err)
	}
	return core.ID(id), nil
}

func (s *Store) GetFeed(ctx context.Context, userID, feedID core.ID) (*core.Feed, error) {
	r, err := s.q.GetFeed(ctx, sqlc.GetFeedParams{ID: int64(feedID), UserID: int64(userID)})
	if err != nil {
		return nil, mapErr(err)
	}
	return feedFromRow(r), nil
}

func (s *Store) ListFeeds(ctx context.Context, userID core.ID) ([]*core.Feed, error) {
	rows, err := s.q.ListFeeds(ctx, int64(userID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]*core.Feed, 0, len(rows))
	for _, r := range rows {
		out = append(out, feedFromRow(r))
	}
	return out, nil
}

func (s *Store) ListDueFeeds(ctx context.Context, now time.Time, limit int) ([]*core.Feed, error) {
	rows, err := s.q.ListDueFeeds(ctx, sqlc.ListDueFeedsParams{
		NextCheckAt: toUnix(now),
		Limit:       int64(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]*core.Feed, 0, len(rows))
	for _, r := range rows {
		out = append(out, feedFromRow(r))
	}
	return out, nil
}

func (s *Store) UpdateFeed(ctx context.Context, f *core.Feed) error {
	return mapErr(s.q.UpdateFeed(ctx, sqlc.UpdateFeedParams{
		SiteUrl:      f.SiteURL,
		Title:        f.Title,
		Description:  f.Description,
		Etag:         f.ETag,
		LastModified: f.LastModified,
		Disabled:     b2i(f.Disabled),
		CheckedAt:    nullUnix(f.CheckedAt),
		NextCheckAt:  toUnix(f.NextCheckAt),
		ErrorCount:   int64(f.ErrorCount),
		LastError:    f.LastError,
		UpdatedAt:    toUnix(f.UpdatedAt),
		ID:           int64(f.ID),
		UserID:       int64(f.UserID),
	}))
}

func (s *Store) DeleteFeed(ctx context.Context, userID, feedID core.ID) error {
	if _, err := s.GetFeed(ctx, userID, feedID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback()
	qtx := s.q.WithTx(tx)
	deletedAt := time.Now().UTC().Unix()
	if err := qtx.DeleteFeedTombstones(ctx, sqlc.DeleteFeedTombstonesParams{
		DeletedAt: deletedAt,
		FeedID:    int64(feedID),
	}); err != nil {
		return mapErr(err)
	}
	if _, err := qtx.DeleteFeed(ctx, sqlc.DeleteFeedParams{
		ID:     int64(feedID),
		UserID: int64(userID),
	}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit())
}
