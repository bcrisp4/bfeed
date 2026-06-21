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

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", nil, false)
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

func TestSubscribeBlankTitleFallsBackToFeedURL(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	// Some feeds ship an empty <title></title> but still have entries, so the
	// feed is accepted with a blank title.
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "  ", Entries: []core.ParsedEntry{
		{GUID: "g1", URL: "https://b.test/1", Title: "P1"},
	}}}
	svc, _ := newFeedSvc(store, fetcher, parser)

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", nil, false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if f.Title != "https://b.test/feed.xml" {
		t.Fatalf("blank title not backfilled: Title=%q, want feed URL", f.Title)
	}
}

func TestPollFeedBlankTitleStaysNonEmpty(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	// Simulate a feed whose stored title is already blank (e.g. pre-fix data).
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/feed.xml", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "", Entries: []core.ParsedEntry{{GUID: "g1", URL: "https://b.test/1"}}}}
	svc, _ := newFeedSvc(store, fetcher, parser)

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.Title != "https://b.test/feed.xml" {
		t.Fatalf("poll left blank title: Title=%q, want feed URL", got.Title)
	}
}

func TestSubscribeDuplicateConflict(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("x")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/f", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/f", nil, false); err == nil {
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

func TestSetFullContentBackfillsAllExistingEntries(t *testing.T) {
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	svc := core.NewFeedService(store, nil, nil, nil, clk, coretest.DiscardLogger(), core.FeedServiceConfig{})
	ctx := context.Background()
	fid, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x/f", NextCheckAt: clk.T, CreatedAt: clk.T, UpdatedAt: clk.T})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"a", "b", "c"} { // none entries: all should be backfilled
		coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: g, URL: "https://x/" + g, PublishedAt: clk.T, CreatedAt: clk.T, ExtractState: core.ExtractNone})
	}
	// failed entry: should also be re-queued by backfill
	coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "d", URL: "https://x/d", PublishedAt: clk.T, CreatedAt: clk.T, ExtractState: core.ExtractFailed})
	// done entry: must NOT become pending after backfill
	coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "e", URL: "https://x/e", PublishedAt: clk.T, CreatedAt: clk.T, ExtractState: core.ExtractDone})
	if err := svc.SetFullContent(ctx, core.DefaultUserID, fid, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if p, _ := store.ListPendingExtractions(ctx, clk.T, 100); len(p) != 4 {
		t.Fatalf("want 4 pending after enable (3 none + 1 failed, skipping done), got %d", len(p))
	}
	if err := svc.SetFullContent(ctx, core.DefaultUserID, fid, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if p, _ := store.ListPendingExtractions(ctx, clk.T, 100); len(p) != 0 {
		t.Fatalf("want 0 pending after disable, got %d", len(p))
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
