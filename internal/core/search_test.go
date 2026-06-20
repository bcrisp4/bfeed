package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

type spyIndex struct {
	called  bool
	gotQ    string
	entries []*core.Entry
}

func (s *spyIndex) Search(_ context.Context, _ core.ID, query string, _ core.EntryFilter) ([]*core.Entry, *core.Cursor, error) {
	s.called = true
	s.gotQ = query
	return s.entries, nil, nil
}

func TestSearchServiceEmptyQueryDoesNotCallIndex(t *testing.T) {
	spy := &spyIndex{}
	svc := core.NewSearchService(spy, coretest.DiscardLogger())
	got, cur, err := svc.Search(context.Background(), core.DefaultUserID, "   ", core.EntryFilter{})
	if err != nil || got != nil || cur != nil {
		t.Fatalf("empty query: got=%v cur=%v err=%v", got, cur, err)
	}
	if spy.called {
		t.Fatal("index was called on a blank query")
	}
}

func TestSearchServiceTrimsAndForwards(t *testing.T) {
	spy := &spyIndex{}
	svc := core.NewSearchService(spy, coretest.DiscardLogger())
	if _, _, err := svc.Search(context.Background(), core.DefaultUserID, "  rust  ", core.EntryFilter{}); err != nil {
		t.Fatal(err)
	}
	if !spy.called {
		t.Fatal("index not called for a real query")
	}
	if spy.gotQ != "rust" {
		t.Fatalf("forwarded query = %q, want trimmed %q", spy.gotQ, "rust")
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
