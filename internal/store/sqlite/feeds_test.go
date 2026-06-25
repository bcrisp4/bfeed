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
	e1 := &core.Entry{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "del-g1", URL: "https://del.test/1",
		Title: "T1", Content: "c", PublishedAt: now, CreatedAt: now, Hash: "h1",
	}
	e2 := &core.Entry{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "del-g2", URL: "https://del.test/2",
		Title: "T2", Content: "c", PublishedAt: now, CreatedAt: now, Hash: "h2",
	}
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

func TestCreateFeedWithCategory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	// Seed a category directly; the CategoryStore Go methods land in Task 2.
	res, err := s.db.ExecContext(ctx, `INSERT INTO categories (user_id, title) VALUES (?, ?)`,
		int64(core.DefaultUserID), "News")
	if err != nil {
		t.Fatalf("seed category: %v", err)
	}
	catID64, _ := res.LastInsertId()
	catID := core.ID(catID64)

	f := &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://x.test/f", CategoryID: &catID,
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	}
	id, err := s.CreateFeed(ctx, f)
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	got, err := s.GetFeed(ctx, core.DefaultUserID, id)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.CategoryID == nil || *got.CategoryID != catID {
		t.Fatalf("CategoryID round-trip = %v, want %d", got.CategoryID, catID)
	}

	// A feed with no category round-trips as nil.
	id2, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://y.test/f",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFeed 2: %v", err)
	}
	got2, _ := s.GetFeed(ctx, core.DefaultUserID, id2)
	if got2.CategoryID != nil {
		t.Fatalf("uncategorised feed CategoryID = %v, want nil", got2.CategoryID)
	}
}

func TestFeedFullContentRoundTripAndToggle(t *testing.T) {
	st, ctx := newTestStore(t), context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	f := &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x.example/feed", NextCheckAt: now, CreatedAt: now, UpdatedAt: now, FetchFullContent: true}
	id, err := st.CreateFeed(ctx, f)
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	got, err := st.GetFeed(ctx, core.DefaultUserID, id)
	if err != nil || !got.FetchFullContent {
		t.Fatalf("want FetchFullContent=true, got %+v err=%v", got, err)
	}
	if err := st.SetFeedFullContent(ctx, core.DefaultUserID, id, false); err != nil {
		t.Fatalf("SetFeedFullContent: %v", err)
	}
	got, _ = st.GetFeed(ctx, core.DefaultUserID, id)
	if got.FetchFullContent {
		t.Fatalf("want FetchFullContent=false after toggle")
	}
	if err := st.SetFeedFullContent(ctx, 999, id, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("wrong-user toggle: want ErrNotFound, got %v", err)
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

func TestEntryStatsByFeed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_100, 0).UTC()
	fA, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	fB, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	ins, err := s.UpsertEntries(ctx, fA, []*core.Entry{mkEntry(fA, "a1", now), mkEntry(fA, "a2", now), mkEntry(fA, "a3", now)})
	if err != nil {
		t.Fatal(err)
	}
	// Mark one of feed A's entries read; feed A is then 3 total / 2 unread.
	if err := s.SetStatus(ctx, core.DefaultUserID, []core.ID{ins[0].ID}, core.StatusRead); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertEntries(ctx, fB, []*core.Entry{mkEntry(fB, "b1", now)}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.EntryStatsByFeed(ctx, core.DefaultUserID)
	if err != nil {
		t.Fatalf("EntryStatsByFeed: %v", err)
	}
	if got := stats[fA]; got.Total != 3 || got.Unread != 2 {
		t.Fatalf("feed A stats = %+v, want {Total:3 Unread:2}", got)
	}
	if got := stats[fB]; got.Total != 1 || got.Unread != 1 {
		t.Fatalf("feed B stats = %+v, want {Total:1 Unread:1}", got)
	}
}

func TestWeeklyEntryCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	fid, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://e.com/f", Title: "f",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	mk := func(guid string, published, created time.Time) *core.Entry {
		return &core.Entry{
			UserID: core.DefaultUserID, FeedID: fid, GUID: guid,
			PublishedAt: published, CreatedAt: created, Status: core.StatusUnread,
		}
	}
	zero := time.Time{}
	_, err = s.UpsertEntries(ctx, fid, []*core.Entry{
		mk("a", now.Add(-2*24*time.Hour), now.Add(-2*24*time.Hour)),
		mk("b", now.Add(-1*24*time.Hour), now.Add(-1*24*time.Hour)),
		mk("c", now.Add(48*time.Hour), now.Add(-1*time.Hour)),         // future published -> excluded
		mk("d", zero, now.Add(-3*time.Hour)),                          // date-less -> counted via created_at
		mk("e", now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour)), // old -> excluded
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.WeeklyEntryCount(ctx, fid, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 { // a, b, d
		t.Fatalf("WeeklyEntryCount = %d, want 3", got)
	}
}

func TestFeedTTLRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	fid, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://e.com/ttl", Title: "t",
		TTL: 45 * time.Minute, NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFeed(ctx, core.DefaultUserID, fid)
	if err != nil {
		t.Fatal(err)
	}
	if got.TTL != 45*time.Minute {
		t.Fatalf("TTL after create = %v, want 45m", got.TTL)
	}

	got.TTL = 2 * time.Hour
	got.UpdatedAt = now
	if err := s.UpdateFeed(ctx, got); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.GetFeed(ctx, core.DefaultUserID, fid)
	if got2.TTL != 2*time.Hour {
		t.Fatalf("TTL after update = %v, want 2h", got2.TTL)
	}
}

func TestSetFeedUserTitle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	id, err := st.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://e.com/f", Title: "Auto", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFeedUserTitle(ctx, core.DefaultUserID, id, "Renamed"); err != nil {
		t.Fatal(err)
	}
	f, err := st.GetFeed(ctx, core.DefaultUserID, id)
	if err != nil {
		t.Fatal(err)
	}
	if f.UserTitle != "Renamed" || f.DisplayTitle() != "Renamed" {
		t.Errorf("got UserTitle=%q display=%q", f.UserTitle, f.DisplayTitle())
	}
	if err := st.SetFeedUserTitle(ctx, core.DefaultUserID, 9999, "x"); err != core.ErrNotFound {
		t.Errorf("want ErrNotFound for unknown feed, got %v", err)
	}
}
