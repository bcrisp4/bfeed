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

func TestDeleteFeedRemovesEntries(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	// Create a feed and upsert 2 entries.
	fid, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://del.test/f",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	e1 := &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "del-g1", URL: "https://del.test/1",
		Title: "T1", Content: "c", PublishedAt: now, CreatedAt: now, Hash: "h1"}
	e2 := &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "del-g2", URL: "https://del.test/2",
		Title: "T2", Content: "c", PublishedAt: now, CreatedAt: now, Hash: "h2"}
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{e1, e2}); err != nil {
		t.Fatalf("UpsertEntries: %v", err)
	}

	// Delete the feed.
	if err := s.DeleteFeed(ctx, core.DefaultUserID, fid); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}

	// Feed must be gone.
	if _, err := s.GetFeed(ctx, core.DefaultUserID, fid); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetFeed after delete = %v, want ErrNotFound", err)
	}

	// Entries must be cascaded away.
	entries, _, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{FeedID: &fid})
	if err != nil {
		t.Fatalf("ListEntries after feed delete: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after feed delete, got %d", len(entries))
	}

	// Deleting a non-existent feed must return ErrNotFound.
	if err := s.DeleteFeed(ctx, core.DefaultUserID, fid); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("double-delete err = %v, want ErrNotFound", err)
	}

	// Deleting with wrong user must return ErrNotFound.
	fid2, _ := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://del2.test/f",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err := s.DeleteFeed(ctx, 999, fid2); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("wrong-user delete err = %v, want ErrNotFound", err)
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
