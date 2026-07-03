package coretest_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// These tests pin MemStore to the real store/sqlite semantics it stands in for.
// A drifted fake lets service/web tests pass against behaviour production never
// exhibits (the "fake that lies" failure mode CLAUDE.md warns about).

func feed(url, title string) *core.Feed {
	return &core.Feed{
		UserID: core.DefaultUserID, FeedURL: url, Title: title,
		NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
	}
}

// UpsertEntries must update an existing (feed_id, guid) row in place when the
// content hash changes, mirroring store/sqlite UpdateEntryContent.
func TestMemStoreUpsertUpdatesOnHashChange(t *testing.T) {
	ctx := context.Background()
	s := coretest.NewMemStore()
	fid, _ := s.CreateFeed(ctx, feed("https://f/x", "F"))

	ins, _ := s.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "old", Content: "<p>old</p>",
		Status: core.StatusUnread, PublishedAt: time.Unix(100, 0), Hash: "h1",
	}})
	if len(ins) != 1 {
		t.Fatalf("first upsert inserted %d, want 1", len(ins))
	}
	id := ins[0].ID
	// Mark read + starred so we can prove the update preserves user state.
	if err := s.SetStatus(ctx, core.DefaultUserID, []core.ID{id}, core.StatusRead); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStarred(ctx, core.DefaultUserID, []core.ID{id}, true); err != nil {
		t.Fatal(err)
	}

	// Re-poll same GUID, changed hash + text.
	ins2, _ := s.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "new", Content: "<p>new</p>",
		Status: core.StatusUnread, PublishedAt: time.Unix(200, 0), Hash: "h2",
	}})
	if len(ins2) != 0 {
		t.Fatalf("re-upsert of existing GUID reported %d inserts, want 0", len(ins2))
	}

	got, err := s.GetEntry(ctx, core.DefaultUserID, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new" || got.Content != "<p>new</p>" || got.Hash != "h2" || !got.PublishedAt.Equal(time.Unix(200, 0)) {
		t.Fatalf("hash-change update not applied: %+v", got)
	}
	// User state must survive the in-place content update.
	if got.Status != core.StatusRead || got.ReadAt == nil || !got.Starred {
		t.Fatalf("in-place update clobbered user state: status=%s read=%v starred=%v", got.Status, got.ReadAt, got.Starred)
	}
}

// Same GUID, same hash → no change (and no phantom insert).
func TestMemStoreUpsertSkipsWhenHashUnchanged(t *testing.T) {
	ctx := context.Background()
	s := coretest.NewMemStore()
	fid, _ := s.CreateFeed(ctx, feed("https://f/x", "F"))
	ins, _ := s.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "t", Status: core.StatusUnread,
		PublishedAt: time.Unix(100, 0), Hash: "h1",
	}})
	id := ins[0].ID
	ins2, _ := s.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "different-but-same-hash",
		Status: core.StatusUnread, PublishedAt: time.Unix(100, 0), Hash: "h1",
	}})
	if len(ins2) != 0 {
		t.Fatalf("unchanged-hash re-upsert reported %d inserts, want 0", len(ins2))
	}
	got, _ := s.GetEntry(ctx, core.DefaultUserID, id)
	if got.Title != "t" {
		t.Fatalf("unchanged-hash upsert mutated the row: %q", got.Title)
	}
}

// ListFeeds must return title-ascending, case-insensitive, like the real query.
func TestMemStoreListFeedsSortedNoCase(t *testing.T) {
	ctx := context.Background()
	s := coretest.NewMemStore()
	s.CreateFeed(ctx, feed("https://f/1", "banana"))
	s.CreateFeed(ctx, feed("https://f/2", "Apple"))
	s.CreateFeed(ctx, feed("https://f/3", "cherry"))

	got, err := s.ListFeeds(ctx, core.DefaultUserID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Apple", "banana", "cherry"}
	if len(got) != len(want) {
		t.Fatalf("got %d feeds, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Title != w {
			t.Fatalf("feed[%d] = %q, want %q (order not NOCASE-ascending)", i, got[i].Title, w)
		}
	}
}

// ListEntries must clamp a limit > 200 back to 50, like the real store.
func TestMemStoreListEntriesClampsLargeLimit(t *testing.T) {
	ctx := context.Background()
	s := coretest.NewMemStore()
	fid, _ := s.CreateFeed(ctx, feed("https://f/x", "F"))
	batch := make([]*core.Entry, 0, 60)
	for i := 0; i < 60; i++ {
		batch = append(batch, &core.Entry{
			UserID: core.DefaultUserID, FeedID: fid, GUID: "g" + strconv.Itoa(i), Title: "t",
			Status: core.StatusUnread, PublishedAt: time.Unix(int64(1000+i), 0), Hash: "h" + strconv.Itoa(i),
		})
	}
	s.UpsertEntries(ctx, fid, batch)

	got, next, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("limit=500 returned %d rows, want 50 (clamped)", len(got))
	}
	if next == nil {
		t.Fatal("clamped page should emit a next cursor when more rows exist")
	}
}

// UpdateFeed on a missing (or other-user) feed must silently no-op, mirroring the
// real unchecked `:exec ... WHERE id = ? AND user_id = ?`.
func TestMemStoreUpdateFeedMissingIsNoOp(t *testing.T) {
	ctx := context.Background()
	s := coretest.NewMemStore()
	// Absent feed.
	if err := s.UpdateFeed(ctx, &core.Feed{ID: 999, UserID: core.DefaultUserID, Title: "x"}); err != nil {
		t.Fatalf("UpdateFeed on missing feed = %v, want nil (silent no-op)", err)
	}
	// Wrong user must not mutate the row.
	fid, _ := s.CreateFeed(ctx, feed("https://f/x", "orig"))
	other := core.ID(2)
	if err := s.UpdateFeed(ctx, &core.Feed{ID: fid, UserID: other, Title: "hijacked"}); err != nil {
		t.Fatalf("UpdateFeed for other user = %v, want nil", err)
	}
	got, _ := s.GetFeed(ctx, core.DefaultUserID, fid)
	if got.Title != "orig" {
		t.Fatalf("UpdateFeed for other user mutated the row: title=%q", got.Title)
	}
}
