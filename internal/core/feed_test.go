package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func newFeedSvc(store core.Store, fetcher core.Fetcher, parser core.FeedParser) (*core.FeedService, coretest.StubClock) {
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	cfg := core.FeedServiceConfig{
		Reschedule: core.RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour},
		Jitter:     func(time.Duration) time.Duration { return 0 },
	}
	return core.NewFeedService(store, fetcher, parser, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(), cfg), clk
}

func TestSubscribeCreatesFeedAndEntries(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>"), ETag: `"e"`}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog", SiteURL: "https://b.test/", Entries: []core.ParsedEntry{
		{GUID: "g1", URL: "https://b.test/1", Title: "P1", Content: "<p>x</p>", PublishedAt: time.Unix(1_700_000_000, 0).UTC()},
	}}}
	svc, clk := newFeedSvc(store, fetcher, parser)

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if f.Title != "Blog" || f.ETag != `"e"` {
		t.Fatalf("feed metadata not set: %+v", f)
	}
	if !f.NextCheckAt.Equal(clk.Now().Add(15 * time.Minute)) {
		t.Fatalf("next check not scheduled: %v", f.NextCheckAt)
	}
	es, _, _ := store.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{})
	if len(es) != 1 {
		t.Fatalf("entries inserted = %d, want 1", len(es))
	}
}

func TestSubscribeDuplicateConflict(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("x")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/f"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/f"); err == nil {
		t.Fatal("expected conflict on duplicate subscribe")
	}
}

func TestPollFeed304ResetsErrorAndReschedules(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", ErrorCount: 3, NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 304, NotModified: true}}
	svc, clk := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{}})
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.ErrorCount != 0 {
		t.Fatalf("error count not reset on 304: %d", got.ErrorCount)
	}
	if !got.NextCheckAt.Equal(clk.Now().Add(15 * time.Minute)) {
		t.Fatalf("304 reschedule wrong: %v", got.NextCheckAt)
	}
}

func TestPollFeedErrorBacksOff(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	fetcher := coretest.StubFetcher{Err: context.DeadlineExceeded}
	svc, clk := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{}})
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed should swallow fetch error, got %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.ErrorCount != 1 || got.LastError == "" {
		t.Fatalf("error not recorded: count=%d err=%q", got.ErrorCount, got.LastError)
	}
	if !got.NextCheckAt.Equal(clk.Now().Add(30 * time.Minute)) { // 15m * 2^1
		t.Fatalf("backoff reschedule wrong: %v", got.NextCheckAt)
	}
}
