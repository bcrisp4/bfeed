package coretest

// White-box test: the tombstone-cascade invariant is invisible through the public
// Store API (MemStore never reuses feed ids, so a stale tombstone can't match a
// re-subscribe), so we assert on internal state directly.

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestMemStoreDeleteFeedCascadesTombstones(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	fid, _ := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://f/x", Title: "F",
		NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
	})
	ins, _ := s.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "t", Status: core.StatusUnread,
		PublishedAt: time.Unix(100, 0), Hash: "h",
	}})
	// Individually deleting the entry writes a (feed_id, guid) tombstone.
	if err := s.DeleteEntry(ctx, core.DefaultUserID, ins[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(s.tombstones) != 1 {
		t.Fatalf("expected 1 tombstone after DeleteEntry, got %d", len(s.tombstones))
	}
	// Deleting the whole feed must cascade the tombstone away (mirrors the FK
	// ON DELETE CASCADE on tombstones.feed_id).
	if err := s.DeleteFeed(ctx, core.DefaultUserID, fid); err != nil {
		t.Fatal(err)
	}
	if len(s.tombstones) != 0 {
		t.Fatalf("DeleteFeed left %d tombstones, want 0 (cascade)", len(s.tombstones))
	}
}
