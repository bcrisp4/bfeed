package core_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func newFeedSvc(store core.Store, fetcher core.Fetcher, parser core.FeedParser) (*core.FeedService, coretest.StubClock) {
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	cfg := core.FeedServiceConfig{
		Schedule:   core.ScheduleConfig{MinInterval: 15 * time.Minute, MaxInterval: 24 * time.Hour, Factor: 1},
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

func TestPollFeed304BackfillsBlankTitle(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	// Pre-fix data: a feed stored with a blank title that now only returns 304.
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/feed.xml", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 304, NotModified: true}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{}})

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.Title != "https://b.test/feed.xml" {
		t.Fatalf("304 poll left blank title: Title=%q, want feed URL", got.Title)
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

func newFeedSvcSched(store core.Store, fetcher core.Fetcher, parser core.FeedParser, sched core.ScheduleConfig) (*core.FeedService, coretest.StubClock) {
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	cfg := core.FeedServiceConfig{
		Schedule:   sched,
		Reschedule: core.RescheduleConfig{Interval: sched.MinInterval, MaxBackoff: sched.MaxInterval},
		Jitter:     func(time.Duration) time.Duration { return 0 },
	}
	return core.NewFeedService(store, fetcher, parser, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(), cfg), clk
}

func sched5m() core.ScheduleConfig {
	return core.ScheduleConfig{MinInterval: 5 * time.Minute, MaxInterval: 24 * time.Hour, Factor: 1}
}

func TestPollColdStartUsesMinInterval(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b/f", Title: "b", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)

	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "b"}}
	svc, clk := newFeedSvcSched(store, fetcher, parser, sched5m())

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if !got.NextCheckAt.Equal(clk.Now().Add(5 * time.Minute)) {
		t.Fatalf("cold-start next = %v, want now+5m", got.NextCheckAt)
	}
}

func TestPollAgedFeedAdapts(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	created := now.Add(-8 * 24 * time.Hour)
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b/f", Title: "b", NextCheckAt: now, CreatedAt: created, UpdatedAt: created})
	es := make([]*core.Entry, 0, 14)
	for i := 0; i < 14; i++ {
		ts := now.Add(-time.Duration(i+1) * time.Hour)
		es = append(es, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: fmt.Sprintf("g%d", i), PublishedAt: ts, CreatedAt: ts, Status: core.StatusUnread})
	}
	if _, err := store.UpsertEntries(ctx, fid, es); err != nil {
		t.Fatal(err)
	}
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)

	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "b"}} // no new entries
	svc, clk := newFeedSvcSched(store, fetcher, parser, sched5m())

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatal(err)
	}
	want := clk.Now().Add(7 * 24 * time.Hour / 14) // 12h
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if !got.NextCheckAt.Equal(want) {
		t.Fatalf("aged next = %v, want %v", got.NextCheckAt, want)
	}
}

func TestPollRefreshesTTL(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b/f", Title: "b", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)

	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "b", TTL: 90 * time.Minute}}
	svc, _ := newFeedSvcSched(store, fetcher, parser, sched5m())

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.TTL != 90*time.Minute {
		t.Fatalf("TTL after poll = %v, want 90m", got.TTL)
	}
}
