package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestCreateAndGetFeed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	f := &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://x.test/feed.xml",
		Title: "X", NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	}
	id, err := s.CreateFeed(ctx, f)
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	got, err := s.GetFeed(ctx, core.DefaultUserID, id)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.FeedURL != f.FeedURL || !got.NextCheckAt.Equal(now) {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestCreateFeedDuplicateURLConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	f := &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now}
	if _, err := s.CreateFeed(ctx, f); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.CreateFeed(ctx, f); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("duplicate err = %v, want ErrConflict", err)
	}
}

func TestGetFeedWrongUserNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	id, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	if _, err := s.GetFeed(ctx, 999, id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-user get err = %v, want ErrNotFound", err)
	}
}

func TestListDueFeeds(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0).UTC()
	mk := func(url string, due time.Time) {
		if _, err := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: url, NextCheckAt: due, CreatedAt: base, UpdatedAt: base}); err != nil {
			t.Fatal(err)
		}
	}
	mk("https://a.test/f", base.Add(-time.Minute)) // due
	mk("https://b.test/f", base.Add(time.Hour))    // not due
	due, err := s.ListDueFeeds(ctx, base, 10)
	if err != nil {
		t.Fatalf("ListDueFeeds: %v", err)
	}
	if len(due) != 1 || due[0].FeedURL != "https://a.test/f" {
		t.Fatalf("due = %+v, want only a.test", due)
	}
}
