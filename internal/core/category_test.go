package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func TestCategoryServiceCreateValidatesAndConflicts(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	svc := core.NewCategoryService(store, coretest.DiscardLogger())

	if _, err := svc.Create(ctx, core.DefaultUserID, "   "); !errors.Is(err, core.ErrValidation) {
		t.Fatalf("blank title err = %v, want ErrValidation", err)
	}
	c, err := svc.Create(ctx, core.DefaultUserID, "News")
	if err != nil || c.ID == 0 {
		t.Fatalf("Create: %+v err=%v", c, err)
	}
	if _, err := svc.Create(ctx, core.DefaultUserID, "News"); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("dup err = %v, want ErrConflict", err)
	}
}

func TestCategoryServiceRenameConflict(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	svc := core.NewCategoryService(store, coretest.DiscardLogger())
	if _, err := svc.Create(ctx, core.DefaultUserID, "News"); err != nil {
		t.Fatal(err)
	}
	tech, err := svc.Create(ctx, core.DefaultUserID, "Tech")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename(ctx, core.DefaultUserID, tech.ID, "News"); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("rename to existing title err = %v, want ErrConflict", err)
	}
}

func TestSubscribeWithCategorySetsIt(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog"}}
	svc, _ := newFeedSvc(store, fetcher, parser)

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", &catID, false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if f.CategoryID == nil || *f.CategoryID != catID {
		t.Fatalf("subscribed feed CategoryID = %v, want %d", f.CategoryID, catID)
	}

	// Subscribing with an unknown category is rejected.
	bad := core.ID(999)
	if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://c.test/feed.xml", &bad, false); !errors.Is(err, core.ErrValidation) {
		t.Fatalf("unknown category err = %v, want ErrValidation", err)
	}
}

// EditFeed is the sole production path that re-homes a feed's category; it must
// enforce category ownership and persist the change (the dead SetCategory method
// that used to do this was removed — EditFeed already inlines the same steps).
func TestEditFeedSetsCategory(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	svc, _ := newFeedSvc(store, coretest.StubFetcher{}, coretest.StubParser{})

	res, err := svc.EditFeed(ctx, core.DefaultUserID, fid, core.EditFeedInput{CategoryID: &catID})
	if err != nil {
		t.Fatalf("EditFeed: %v", err)
	}
	if !res.CategoryChanged {
		t.Fatal("expected CategoryChanged")
	}
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if f.CategoryID == nil || *f.CategoryID != catID {
		t.Fatalf("not assigned: %v", f.CategoryID)
	}
	bad := core.ID(999)
	if _, err := svc.EditFeed(ctx, core.DefaultUserID, fid, core.EditFeedInput{CategoryID: &bad}); !errors.Is(err, core.ErrValidation) {
		t.Fatalf("unknown category err = %v, want ErrValidation", err)
	}
}

func TestEntryListFiltersByCategory(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(100, 0)
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fCat, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", CategoryID: &catID, NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	fUncat, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	store.UpsertEntries(ctx, fCat, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fCat, GUID: "c1", Status: core.StatusUnread, PublishedAt: now}})
	store.UpsertEntries(ctx, fUncat, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fUncat, GUID: "u1", Status: core.StatusUnread, PublishedAt: now}})
	es := core.NewEntryService(store, coretest.DiscardLogger())

	inCat, _, _ := es.List(ctx, core.DefaultUserID, core.EntryFilter{CategoryID: &catID})
	if len(inCat) != 1 || inCat[0].FeedID != fCat {
		t.Fatalf("category filter got %d (want 1 from fCat)", len(inCat))
	}
	uncat, _, _ := es.List(ctx, core.DefaultUserID, core.EntryFilter{Uncategorised: true})
	if len(uncat) != 1 || uncat[0].FeedID != fUncat {
		t.Fatalf("uncategorised filter got %d (want 1 from fUncat)", len(uncat))
	}
}
