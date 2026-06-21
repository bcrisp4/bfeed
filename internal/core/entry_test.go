package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func TestEntryServiceMarkReadAndStar(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Status: core.StatusUnread, PublishedAt: time.Unix(10, 0)}})
	svc := core.NewEntryService(store, coretest.DiscardLogger())
	id := ins[0].ID
	if err := svc.MarkRead(ctx, core.DefaultUserID, []core.ID{id}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Star(ctx, core.DefaultUserID, []core.ID{id}, true); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, core.DefaultUserID, id)
	if got.Status != core.StatusRead || !got.Starred {
		t.Fatalf("mark/star not applied: %+v", got)
	}
}

func TestEntryServiceDeleteScopedToUser(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Status: core.StatusUnread, PublishedAt: time.Unix(10, 0)}})
	svc := core.NewEntryService(store, coretest.DiscardLogger())
	if err := svc.Delete(ctx, 999, ins[0].ID); err == nil {
		t.Fatal("cross-user delete should fail")
	}
	if err := svc.Delete(ctx, core.DefaultUserID, ins[0].ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestEntryServiceMarkAllReadByFeed(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	other, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/g", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "a", Status: core.StatusUnread, PublishedAt: time.Unix(10, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "b", Status: core.StatusUnread, PublishedAt: time.Unix(20, 0)},
	})
	insOther, _ := store.UpsertEntries(ctx, other, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: other, GUID: "c", Status: core.StatusUnread, PublishedAt: time.Unix(30, 0)},
	})
	svc := core.NewEntryService(store, coretest.DiscardLogger())

	n, err := svc.MarkAllRead(ctx, core.DefaultUserID, core.EntryFilter{FeedID: &fid})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("MarkAllRead affected %d, want 2", n)
	}
	for _, id := range []core.ID{ins[0].ID, ins[1].ID} {
		e, _ := svc.Get(ctx, core.DefaultUserID, id)
		if e.Status != core.StatusRead || e.ReadAt == nil {
			t.Fatalf("entry %d not read with read_at: %+v", id, e)
		}
	}
	eo, _ := svc.Get(ctx, core.DefaultUserID, insOther[0].ID)
	if eo.Status != core.StatusUnread {
		t.Fatalf("other feed entry should stay unread: %+v", eo)
	}
}

func TestEntryServiceMarkAllReadAllFeeds(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	gid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/g", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "a", Status: core.StatusUnread, PublishedAt: time.Unix(10, 0)},
	})
	insG, _ := store.UpsertEntries(ctx, gid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: gid, GUID: "b", Status: core.StatusUnread, PublishedAt: time.Unix(20, 0)},
	})
	svc := core.NewEntryService(store, coretest.DiscardLogger())

	n, err := svc.MarkAllRead(ctx, core.DefaultUserID, core.EntryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("MarkAllRead affected %d, want 2", n)
	}
	for _, id := range []core.ID{ins[0].ID, insG[0].ID} {
		e, _ := svc.Get(ctx, core.DefaultUserID, id)
		if e.Status != core.StatusRead || e.ReadAt == nil {
			t.Fatalf("entry %d not read with read_at: %+v", id, e)
		}
	}
}
