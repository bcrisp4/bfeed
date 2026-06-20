package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func TestSearchServiceEmptyQueryShortCircuits(t *testing.T) {
	svc := core.NewSearchService(coretest.NewMemStore(), coretest.DiscardLogger())
	got, cur, err := svc.Search(context.Background(), core.DefaultUserID, "   ", core.EntryFilter{})
	if err != nil || got != nil || cur != nil {
		t.Fatalf("empty query: got=%v cur=%v err=%v", got, cur, err)
	}
}

func TestSearchServiceForwardsToIndex(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fid, _ := store.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://f/x",
		NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
	})
	store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "a", Title: "Rust async runtime", PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "b", Title: "Go scheduler", PublishedAt: time.Unix(200, 0)},
	})
	svc := core.NewSearchService(store, coretest.DiscardLogger())
	got, _, err := svc.Search(ctx, core.DefaultUserID, "rust", core.EntryFilter{})
	if err != nil || len(got) != 1 || got[0].GUID != "a" {
		t.Fatalf("search rust: got=%d err=%v", len(got), err)
	}
}
