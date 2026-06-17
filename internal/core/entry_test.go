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
