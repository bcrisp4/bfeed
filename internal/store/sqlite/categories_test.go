package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestCategoryCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	got, err := s.GetCategory(ctx, core.DefaultUserID, id)
	if err != nil || got.Title != "News" {
		t.Fatalf("GetCategory = %+v err=%v", got, err)
	}
	if err := s.UpdateCategory(ctx, &core.Category{ID: id, UserID: core.DefaultUserID, Title: "World"}); err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	got, err = s.GetCategory(ctx, core.DefaultUserID, id)
	if err != nil {
		t.Fatalf("GetCategory after update: %v", err)
	}
	if got.Title != "World" {
		t.Fatalf("rename not applied: %q", got.Title)
	}
	cats, err := s.ListCategories(ctx, core.DefaultUserID)
	if err != nil || len(cats) != 1 {
		t.Fatalf("ListCategories = %d err=%v", len(cats), err)
	}
	if err := s.DeleteCategory(ctx, core.DefaultUserID, id); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}
	cats, err = s.ListCategories(ctx, core.DefaultUserID)
	if err != nil || len(cats) != 0 {
		t.Fatalf("after delete, ListCategories = %d err=%v", len(cats), err)
	}
	if err := s.DeleteCategory(ctx, core.DefaultUserID, id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestCreateCategoryDuplicateConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := &core.Category{UserID: core.DefaultUserID, Title: "News"}
	if _, err := s.CreateCategory(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCategory(ctx, c); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("duplicate err = %v, want ErrConflict", err)
	}
}

func TestDeleteCategorySetsFeedsNull(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	catID, _ := s.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://a.test/f", CategoryID: &catID,
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err := s.DeleteCategory(ctx, core.DefaultUserID, catID); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}
	f, err := s.GetFeed(ctx, core.DefaultUserID, fid)
	if err != nil {
		t.Fatalf("GetFeed after delete: %v", err)
	}
	if f.CategoryID != nil {
		t.Fatalf("feed not re-homed to uncategorised: %v", f.CategoryID)
	}
}

func TestSetFeedCategory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	catID, _ := s.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://a.test/f",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err := s.SetFeedCategory(ctx, core.DefaultUserID, fid, &catID); err != nil {
		t.Fatalf("SetFeedCategory: %v", err)
	}
	f, err := s.GetFeed(ctx, core.DefaultUserID, fid)
	if err != nil {
		t.Fatalf("GetFeed after assign: %v", err)
	}
	if f.CategoryID == nil || *f.CategoryID != catID {
		t.Fatalf("assign failed: %v", f.CategoryID)
	}
	if err := s.SetFeedCategory(ctx, core.DefaultUserID, fid, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	f, err = s.GetFeed(ctx, core.DefaultUserID, fid)
	if err != nil {
		t.Fatalf("GetFeed after clear: %v", err)
	}
	if f.CategoryID != nil {
		t.Fatalf("clear failed: %v", f.CategoryID)
	}
	// Wrong user → ErrNotFound.
	if err := s.SetFeedCategory(ctx, 999, fid, &catID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-user set err = %v, want ErrNotFound", err)
	}
}

func TestUpdateCategoryNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	err := s.UpdateCategory(ctx, &core.Category{ID: 99999, UserID: core.DefaultUserID, Title: "X"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateCategory non-existent err = %v, want ErrNotFound", err)
	}
}

func TestGetCategoryNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	_, err := s.GetCategory(ctx, core.DefaultUserID, 99999)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetCategory non-existent err = %v, want ErrNotFound", err)
	}
}

func TestUnreadCountsByCategory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_100, 0).UTC()
	catID, _ := s.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fCat, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a.test/f", CategoryID: &catID, NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	fUncat, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	if _, err := s.UpsertEntries(ctx, fCat, []*core.Entry{mkEntry(fCat, "c1", now), mkEntry(fCat, "c2", now)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertEntries(ctx, fUncat, []*core.Entry{mkEntry(fUncat, "u1", now)}); err != nil {
		t.Fatal(err)
	}
	perCat, uncat, err := s.UnreadCountsByCategory(ctx, core.DefaultUserID)
	if err != nil {
		t.Fatalf("UnreadCountsByCategory: %v", err)
	}
	if perCat[catID] != 2 {
		t.Fatalf("category count = %d, want 2", perCat[catID])
	}
	if uncat != 1 {
		t.Fatalf("uncategorised count = %d, want 1", uncat)
	}
}
