package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

type CategoryService struct {
	store CategoryStore
	log   *slog.Logger
}

func NewCategoryService(store CategoryStore, log *slog.Logger) *CategoryService {
	return &CategoryService{store: store, log: log}
}

func (s *CategoryService) Create(ctx context.Context, userID ID, title string) (*Category, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("%w: category title required", ErrValidation)
	}
	c := &Category{UserID: userID, Title: title}
	id, err := s.store.CreateCategory(ctx, c)
	if err != nil {
		return nil, err
	}
	c.ID = id
	return c, nil
}

func (s *CategoryService) List(ctx context.Context, userID ID) ([]*Category, error) {
	return s.store.ListCategories(ctx, userID)
}

func (s *CategoryService) Get(ctx context.Context, userID, id ID) (*Category, error) {
	return s.store.GetCategory(ctx, userID, id)
}

func (s *CategoryService) Rename(ctx context.Context, userID, id ID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("%w: category title required", ErrValidation)
	}
	return s.store.UpdateCategory(ctx, &Category{ID: id, UserID: userID, Title: title})
}

func (s *CategoryService) Delete(ctx context.Context, userID, id ID) error {
	return s.store.DeleteCategory(ctx, userID, id)
}

func (s *CategoryService) UnreadCounts(ctx context.Context, userID ID) (map[ID]int, int, error) {
	return s.store.UnreadCountsByCategory(ctx, userID)
}
