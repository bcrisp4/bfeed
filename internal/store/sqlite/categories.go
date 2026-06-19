package sqlite

import (
	"context"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/store/sqlite/sqlc"
)

func categoryFromRow(r sqlc.Category) *core.Category {
	return &core.Category{ID: core.ID(r.ID), UserID: core.ID(r.UserID), Title: r.Title}
}

func (s *Store) CreateCategory(ctx context.Context, c *core.Category) (core.ID, error) {
	id, err := s.q.CreateCategory(ctx, sqlc.CreateCategoryParams{UserID: int64(c.UserID), Title: c.Title})
	if err != nil {
		return 0, mapErr(err)
	}
	return core.ID(id), nil
}

func (s *Store) GetCategory(ctx context.Context, userID, id core.ID) (*core.Category, error) {
	r, err := s.q.GetCategory(ctx, sqlc.GetCategoryParams{ID: int64(id), UserID: int64(userID)})
	if err != nil {
		return nil, mapErr(err)
	}
	return categoryFromRow(r), nil
}

func (s *Store) ListCategories(ctx context.Context, userID core.ID) ([]*core.Category, error) {
	rows, err := s.q.ListCategories(ctx, int64(userID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]*core.Category, 0, len(rows))
	for _, r := range rows {
		out = append(out, categoryFromRow(r))
	}
	return out, nil
}

func (s *Store) UpdateCategory(ctx context.Context, c *core.Category) error {
	n, err := s.q.UpdateCategory(ctx, sqlc.UpdateCategoryParams{Title: c.Title, ID: int64(c.ID), UserID: int64(c.UserID)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return core.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCategory(ctx context.Context, userID, id core.ID) error {
	n, err := s.q.DeleteCategory(ctx, sqlc.DeleteCategoryParams{ID: int64(id), UserID: int64(userID)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return core.ErrNotFound
	}
	return nil
}

func (s *Store) SetFeedCategory(ctx context.Context, userID, feedID core.ID, categoryID *core.ID) error {
	n, err := s.q.SetFeedCategory(ctx, sqlc.SetFeedCategoryParams{
		CategoryID: nullID(categoryID), ID: int64(feedID), UserID: int64(userID),
	})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return core.ErrNotFound
	}
	return nil
}

func (s *Store) UnreadCountsByCategory(ctx context.Context, userID core.ID) (map[core.ID]int, int, error) {
	rows, err := s.q.UnreadCountsByCategory(ctx, int64(userID))
	if err != nil {
		return nil, 0, mapErr(err)
	}
	perCat := make(map[core.ID]int, len(rows))
	var uncat int
	for _, r := range rows {
		if r.CategoryID.Valid {
			perCat[core.ID(r.CategoryID.Int64)] = int(r.N)
		} else {
			uncat = int(r.N)
		}
	}
	return perCat, uncat, nil
}
