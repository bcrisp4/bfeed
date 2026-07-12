package core_test

import (
	"context"
	"errors"
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

// recordingParser captures the base URL passed to Parse so a test can assert the
// post-redirect FinalURL (not the pre-redirect request URL) is used for resolution.
type recordingParser struct {
	pf       *core.ParsedFeed
	baseSeen string
}

func (p *recordingParser) Parse(_ []byte, _, base string) (*core.ParsedFeed, error) {
	p.baseSeen = base
	return p.pf, nil
}
func (p *recordingParser) Discover([]byte, string) ([]string, error) { return nil, nil }

func TestIngestSanitizesContentAgainstFinalURL(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const orig = "https://old.test/feed.xml"
	const finalURL = "https://mirror.test/feed.xml"
	// A temporary redirect: identity is NOT adopted, but relative links in entry
	// content must still resolve against where the bytes came from (finalURL).
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, Body: []byte("<rss/>"), FinalURL: finalURL, PermanentRedirect: false,
	}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "B", Entries: []core.ParsedEntry{
		{GUID: "g1", URL: finalURL + "/1", Content: `<img src="/x.png">`},
	}}}
	san := &recordingSanitizer{}
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	cfg := core.FeedServiceConfig{
		Schedule:   core.ScheduleConfig{MinInterval: 15 * time.Minute, MaxInterval: 24 * time.Hour, Factor: 1},
		Reschedule: core.RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour},
		Jitter:     func(time.Duration) time.Duration { return 0 },
	}
	svc := core.NewFeedService(store, fetcher, parser, san, clk, coretest.DiscardLogger(), cfg)
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: orig, Title: "B"})

	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	if san.baseURL != finalURL {
		t.Fatalf("Sanitize base = %q, want final URL %q (not pre-redirect %q)", san.baseURL, finalURL, orig)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.FeedURL != orig {
		t.Fatalf("temporary redirect wrongly adopted: feed_url = %q, want %q", got.FeedURL, orig)
	}
}

func seedFeed(t *testing.T, store *coretest.MemStore, f *core.Feed) core.ID {
	t.Helper()
	id, err := store.CreateFeed(context.Background(), f)
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	return id
}

func TestResolveFeedUsesFinalURLBase(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const finalURL = "https://canonical.test/feed.xml"
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>"), FinalURL: finalURL}}
	parser := &recordingParser{pf: &core.ParsedFeed{Title: "B"}}
	svc, _ := newFeedSvc(store, fetcher, parser)

	if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://old.test/feed.xml", nil, false); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if parser.baseSeen != finalURL {
		t.Fatalf("Parse base = %q, want final URL %q", parser.baseSeen, finalURL)
	}
}

func TestPollAdoptsPermanentRedirect(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const canonical = "https://new.test/feed.xml"
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, Body: []byte("<rss/>"), FinalURL: canonical, PermanentRedirect: true,
	}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}})
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://old.test/feed.xml", Title: "B"})

	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.FeedURL != canonical {
		t.Fatalf("feed_url = %q, want adopted canonical %q", got.FeedURL, canonical)
	}
}

func TestPollTemporaryRedirectKeepsURL(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, Body: []byte("<rss/>"), FinalURL: "https://mirror.test/feed.xml", PermanentRedirect: false,
	}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}})
	const orig = "https://old.test/feed.xml"
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: orig, Title: "B"})

	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.FeedURL != orig {
		t.Fatalf("feed_url = %q, want unchanged %q (temporary redirect)", got.FeedURL, orig)
	}
}

func TestPollRedirectConflictKeepsOldURL(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const canonical = "https://new.test/feed.xml"
	const orig = "https://old.test/feed.xml"
	// A feed already exists at the canonical URL, so adopting it must conflict.
	seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: canonical, Title: "Other"})
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, Body: []byte("<rss/>"), FinalURL: canonical, PermanentRedirect: true,
	}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}})
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: orig, Title: "B"})

	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.FeedURL != orig {
		t.Fatalf("feed_url = %q, want kept %q on conflict", got.FeedURL, orig)
	}
	if got.ErrorCount != 0 || got.LastError != "" {
		t.Fatalf("poll recorded an error on redirect conflict: count=%d err=%q", got.ErrorCount, got.LastError)
	}
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

// B10 #6: re-enabling full content must give a previously terminally-'failed'
// entry a fresh attempt budget — MarkFeedEntriesPending resets extract_attempts to
// 0, else a single new failure re-terminates it (3+1 >= cap).
func TestSetFullContentResetsFailedAttempts(t *testing.T) {
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	svc := core.NewFeedService(store, nil, nil, nil, clk, coretest.DiscardLogger(), core.FeedServiceConfig{})
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x/f", NextCheckAt: clk.T, CreatedAt: clk.T, UpdatedAt: clk.T})
	eid := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "d", URL: "https://x/d", PublishedAt: clk.T, CreatedAt: clk.T, ExtractState: core.ExtractFailed, ExtractAttempts: 3, ExtractError: "boom"})

	if err := svc.SetFullContent(ctx, core.DefaultUserID, fid, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got, _ := store.GetEntry(ctx, core.DefaultUserID, eid)
	if got.ExtractState != core.ExtractPending || got.ExtractAttempts != 0 {
		t.Fatalf("re-queued failed entry: want pending attempts=0, got %q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
	if got.ExtractError != "" {
		t.Fatalf("stale extract_error lingered on re-queue: %q", got.ExtractError)
	}
}

// B10 review: disabling full content clears a terminally-'failed' entry's state and
// reason, so the reader stops showing an extraction-failed note for a feed the user
// has turned extraction off for.
func TestSetFullContentDisableClearsFailedState(t *testing.T) {
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	svc := core.NewFeedService(store, nil, nil, nil, clk, coretest.DiscardLogger(), core.FeedServiceConfig{})
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x/f", NextCheckAt: clk.T, CreatedAt: clk.T, UpdatedAt: clk.T, FetchFullContent: true})
	eid := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "d", URL: "https://x/d", PublishedAt: clk.T, CreatedAt: clk.T, ExtractState: core.ExtractFailed, ExtractAttempts: 3, ExtractError: "boom"})

	if err := svc.SetFullContent(ctx, core.DefaultUserID, fid, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _ := store.GetEntry(ctx, core.DefaultUserID, eid)
	if got.ExtractState != core.ExtractNone || got.ExtractError != "" {
		t.Fatalf("disable did not clear failed state: state=%q err=%q", got.ExtractState, got.ExtractError)
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

func TestCreateSubscriptionPersistsWithoutFetch(t *testing.T) {
	st := coretest.NewMemStore()
	// Fetcher that fails if called — proves CreateSubscription does no network I/O.
	fetcher := coretest.StubFetcher{Err: errors.New("must not fetch")}
	svc := core.NewFeedService(st, fetcher, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{T: time.Unix(1000, 0)}, coretest.DiscardLogger(), core.FeedServiceConfig{})

	f, err := svc.CreateSubscription(context.Background(), core.DefaultUserID, "https://example.com/feed.xml", nil, false)
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("expected persisted feed with id")
	}
	if f.CheckedAt != nil {
		t.Error("new subscription must have CheckedAt == nil (pending)")
	}
	if f.DisplayTitle() != "https://example.com/feed.xml" {
		t.Errorf("pending title should fall back to URL, got %q", f.DisplayTitle())
	}
}

func TestCreateSubscriptionRejectsBadURL(t *testing.T) {
	st := coretest.NewMemStore()
	svc := core.NewFeedService(st, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{T: time.Unix(1000, 0)}, coretest.DiscardLogger(), core.FeedServiceConfig{})
	if _, err := svc.CreateSubscription(context.Background(), core.DefaultUserID, "not-a-url", nil, false); !errors.Is(err, core.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

// fixedFetcher always returns the same response, regardless of URL.
type fixedFetcher struct{ resp *core.FetchResponse }

func (f fixedFetcher) Fetch(_ context.Context, _ core.FetchRequest) (*core.FetchResponse, error) {
	return f.resp, nil
}

// setFeedURLErrStore wraps MemStore and always returns an error from SetFeedURL.
type setFeedURLErrStore struct {
	*coretest.MemStore
}

func (s *setFeedURLErrStore) SetFeedURL(ctx context.Context, u, feedID core.ID, url string, now time.Time) error {
	return errors.New("db locked")
}

// discoveryParser returns a parse error for the original URL so resolveFeed
// falls into the Discover path, then succeeds for the discovered URL.
type discoveryParser struct {
	discoveredURL string
	feed          *core.ParsedFeed
}

func (p discoveryParser) Parse(_ []byte, _, feedURL string) (*core.ParsedFeed, error) {
	if feedURL == p.discoveredURL {
		return p.feed, nil
	}
	return nil, errors.New("not a feed")
}

func (p discoveryParser) Discover(_ []byte, _ string) ([]string, error) {
	return []string{p.discoveredURL}, nil
}

func TestResolveAndIngestSetFeedURLErrorRecordsError(t *testing.T) {
	ctx := context.Background()
	const originalURL = "https://example.com/page"
	const discoveredURL = "https://example.com/feed.xml"

	inner := coretest.NewMemStore()
	store := &setFeedURLErrStore{inner}

	// Both fetches (original URL → HTML, discovered URL → feed body) succeed.
	fetcher := fixedFetcher{resp: &core.FetchResponse{Status: 200, Body: []byte("body")}}
	parser := discoveryParser{
		discoveredURL: discoveredURL,
		feed:          &core.ParsedFeed{Title: "Blog"},
	}

	svc, _ := newFeedSvc(store, fetcher, parser)

	now := coretest.StubClock{T: time.Unix(1_700_000_000, 0)}.T
	fid, err := inner.CreateFeed(ctx, &core.Feed{
		UserID:      core.DefaultUserID,
		FeedURL:     originalURL,
		Title:       originalURL,
		NextCheckAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := inner.GetFeed(ctx, core.DefaultUserID, fid)

	_ = svc.ResolveAndIngest(ctx, f)

	got, err := inner.GetFeed(ctx, core.DefaultUserID, fid)
	if err != nil {
		t.Fatalf("feed must still exist after SetFeedURL error: %v", err)
	}
	if got.ErrorCount == 0 {
		t.Errorf("ErrorCount = 0, want > 0")
	}
	if got.LastError == "" {
		t.Errorf("LastError is empty, want error message")
	}
}

// B8/F1: when ResolveAndIngest discovers (here via permanent redirect) a feed
// already subscribed under another row, SetFeedURL conflicts and the just-created
// duplicate row must be deleted — not kept as an error row that re-polls the
// non-feed URL forever.
func TestResolveAndIngestConflictDeletesDuplicateRow(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const canonical = "https://example.com/feed.xml"
	const orig = "https://example.com/site"
	// The user already subscribes to the canonical feed.
	existing := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: canonical, Title: "Existing"})
	// A second subscribe to the site URL resolves (permanent redirect) to canonical.
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, Body: []byte("<rss/>"), FinalURL: canonical, PermanentRedirect: true,
	}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}})
	dupID := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: orig, Title: "B"})

	f, _ := store.GetFeed(ctx, core.DefaultUserID, dupID)
	if err := svc.ResolveAndIngest(ctx, f); err != nil {
		t.Fatalf("ResolveAndIngest returned hard error: %v", err)
	}

	if _, err := store.GetFeed(ctx, core.DefaultUserID, dupID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("duplicate row must be deleted on conflict, got err=%v", err)
	}
	if _, err := store.GetFeed(ctx, core.DefaultUserID, existing); err != nil {
		t.Fatalf("original subscription must survive: %v", err)
	}
}

// deleteFeedErrStore wraps MemStore and always fails DeleteFeed.
type deleteFeedErrStore struct {
	*coretest.MemStore
}

func (s *deleteFeedErrStore) DeleteFeed(context.Context, core.ID, core.ID) error {
	return errors.New("db locked")
}

// B8/F1: if deleting the conflicted duplicate row fails, ResolveAndIngest must
// not silently return nil (leaving the row due, re-polling the non-feed URL) —
// it degrades to recording an error so the row backs off.
func TestResolveAndIngestConflictDeleteFailureRecordsError(t *testing.T) {
	ctx := context.Background()
	inner := coretest.NewMemStore()
	store := &deleteFeedErrStore{inner}
	const canonical = "https://example.com/feed.xml"
	seedFeed(t, inner, &core.Feed{UserID: core.DefaultUserID, FeedURL: canonical, Title: "Existing"})
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, Body: []byte("<rss/>"), FinalURL: canonical, PermanentRedirect: true,
	}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}})
	dupID := seedFeed(t, inner, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://example.com/site", Title: "B"})

	f, _ := inner.GetFeed(ctx, core.DefaultUserID, dupID)
	if err := svc.ResolveAndIngest(ctx, f); err != nil {
		t.Fatalf("ResolveAndIngest returned hard error: %v", err)
	}
	got, err := inner.GetFeed(ctx, core.DefaultUserID, dupID)
	if err != nil {
		t.Fatalf("row must still exist after failed delete: %v", err)
	}
	if got.ErrorCount == 0 || got.LastError == "" {
		t.Fatalf("delete failure must record an error: count=%d err=%q", got.ErrorCount, got.LastError)
	}
}

// B8/F2: trivially different spellings of the same feed URL (case, default port,
// fragment) must normalize to one canonical form so the UNIQUE(user_id, feed_url)
// constraint dedupes them instead of creating a duplicate subscription.
func TestCreateSubscriptionNormalizesForDedupe(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{Title: "B"}})

	if _, err := svc.CreateSubscription(ctx, core.DefaultUserID, "https://Example.COM/feed.xml", nil, false); err != nil {
		t.Fatalf("first CreateSubscription: %v", err)
	}
	_, err := svc.CreateSubscription(ctx, core.DefaultUserID, "https://example.com:443/feed.xml#top", nil, false)
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("second (equivalent) subscription must conflict, got %v", err)
	}
}

func TestResolveAndIngestRecordsErrorKeepsRow(t *testing.T) {
	st := coretest.NewMemStore()
	clk := coretest.StubClock{T: time.Unix(1000, 0)}
	svc := core.NewFeedService(st, coretest.StubFetcher{Err: errors.New("dead host")}, coretest.StubParser{}, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(), core.FeedServiceConfig{})
	f, err := svc.CreateSubscription(context.Background(), core.DefaultUserID, "https://dead.example/feed", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = svc.ResolveAndIngest(context.Background(), f)
	got, err := st.GetFeed(context.Background(), core.DefaultUserID, f.ID)
	if err != nil {
		t.Fatalf("feed must still exist after failed resolve: %v", err)
	}
	if got.ErrorCount == 0 || got.LastError == "" {
		t.Errorf("expected error recorded, got count=%d err=%q", got.ErrorCount, got.LastError)
	}
}

func TestEditFeedAppliesFields(t *testing.T) {
	st := coretest.NewMemStore()
	clk := coretest.StubClock{T: time.Unix(1000, 0)}
	svc := core.NewFeedService(st, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(), core.FeedServiceConfig{})
	f, _ := svc.CreateSubscription(context.Background(), core.DefaultUserID, "https://e.com/old", nil, false)

	res, err := svc.EditFeed(context.Background(), core.DefaultUserID, f.ID, core.EditFeedInput{
		Title: "Renamed", URL: "https://e.com/new", CategoryID: nil, FullContent: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.URLChanged || res.CategoryChanged {
		t.Errorf("got %+v, want URLChanged only", res)
	}
	got, _ := st.GetFeed(context.Background(), core.DefaultUserID, f.ID)
	if got.UserTitle != "Renamed" || got.FeedURL != "https://e.com/new" {
		t.Errorf("got title=%q url=%q", got.UserTitle, got.FeedURL)
	}
}

func TestEditFeedRejectsBadURL(t *testing.T) {
	st := coretest.NewMemStore()
	svc := core.NewFeedService(st, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{T: time.Unix(1, 0)}, coretest.DiscardLogger(), core.FeedServiceConfig{})
	f, _ := svc.CreateSubscription(context.Background(), core.DefaultUserID, "https://e.com/x", nil, false)
	if _, err := svc.EditFeed(context.Background(), core.DefaultUserID, f.ID, core.EditFeedInput{URL: "javascript:alert(1)"}); !errors.Is(err, core.ErrValidation) {
		t.Errorf("want ErrValidation, got %v", err)
	}
}

// B4/F1: an item with no parseable date (PublishedAt left zero by the parser)
// must be ingested with the first-seen (ingest) time, not the year-1 zero value
// that sinks it to the permanent bottom of every published-desc list.
func TestIngestSubstitutesIngestTimeForUndatedEntries(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog", Entries: []core.ParsedEntry{
		{GUID: "g1", URL: "https://b.test/1", Title: "undated"}, // PublishedAt zero
	}}}
	svc, clk := newFeedSvc(store, fetcher, parser)

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", nil, false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_ = f
	es, _, _ := store.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{})
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1", len(es))
	}
	if es[0].PublishedAt.IsZero() {
		t.Fatal("undated entry persisted with zero PublishedAt (sinks to bottom of every list)")
	}
	if !es[0].PublishedAt.Equal(clk.Now()) {
		t.Fatalf("PublishedAt = %v, want ingest time %v", es[0].PublishedAt, clk.Now())
	}
}

// The comments URL comes from untrusted feed content and is rendered as a plain
// href, so ingest keeps only absolute http(s) URLs — same posture as the feed-URL
// check in CreateSubscription.
func TestIngestDropsNonHTTPCommentsURL(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"https kept", "https://news.test/1", "https://news.test/1"},
		{"http kept", "http://news.test/1", "http://news.test/1"},
		{"javascript dropped", "javascript:alert(1)", ""},
		{"data dropped", "data:text/html,x", ""},
		{"ftp dropped", "ftp://news.test/1", ""},
		{"scheme-relative dropped", "//news.test/1", ""},
		{"garbage dropped", "not a url", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := coretest.NewMemStore()
			fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
			parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog", Entries: []core.ParsedEntry{
				{GUID: "g1", URL: "https://b.test/1", Title: "a", Hash: "h1", CommentsURL: tc.raw},
			}}}
			svc, _ := newFeedSvc(store, fetcher, parser)
			if _, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", nil, false); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
			es, _, _ := store.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{})
			if len(es) != 1 {
				t.Fatalf("entries = %d, want 1", len(es))
			}
			if es[0].CommentsURL != tc.want {
				t.Fatalf("CommentsURL = %q, want %q", es[0].CommentsURL, tc.want)
			}
		})
	}
}

// CommentsURL is outside the entry hash, so it refreshes exactly like URL: only
// when the entry's content hash changes on a re-poll.
func TestIngestRefreshesCommentsURLOnHashChange(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	pf := &core.ParsedFeed{Title: "Blog", Entries: []core.ParsedEntry{
		{GUID: "g1", URL: "https://b.test/1", Title: "a", Hash: "h1", CommentsURL: "https://news.test/1"},
	}}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: pf})

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", nil, false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Same GUID, changed content hash, new comments URL: value must refresh.
	pf.Entries[0].Title = "a2"
	pf.Entries[0].Hash = "h2"
	pf.Entries[0].CommentsURL = "https://news.test/2"
	got, _ := store.GetFeed(ctx, core.DefaultUserID, f.ID)
	if err := svc.PollFeed(ctx, got); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	es, _, _ := store.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{})
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1", len(es))
	}
	if es[0].CommentsURL != "https://news.test/2" {
		t.Fatalf("CommentsURL = %q, want refreshed value", es[0].CommentsURL)
	}
}

// B4/F3: a store-layer ingest failure must still reschedule the feed with
// backoff (via recordError), not return early leaving next_check_at in the past
// — otherwise ListDueFeeds re-dispatches the feed (full-body) every tick.
func TestPollReschedulesWhenIngestFails(t *testing.T) {
	ctx := context.Background()
	store := upsertErrStore{coretest.NewMemStore()}
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/feed.xml", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog", Entries: []core.ParsedEntry{{GUID: "g1", URL: "https://b.test/1"}}}}
	svc, _ := newFeedSvc(store, fetcher, parser)

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if !got.NextCheckAt.After(now) {
		t.Fatalf("next_check_at = %v, not advanced past %v — feed will hot-loop", got.NextCheckAt, now)
	}
	if got.ErrorCount != 1 || got.LastError == "" {
		t.Fatalf("ingest failure not recorded as an error: count=%d err=%q", got.ErrorCount, got.LastError)
	}
}

// B4/F4: a syntactically valid feed with a blank title and no items (a freshly
// created feed) must resolve cleanly, not be rejected as "no feed found at URL"
// (which ResolveAndIngest records as a feed error, leaving it permanently stalled).
func TestSubscribeAcceptsValidEmptyFeed(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	// Parser positively identifies a feed but it has no title and no entries yet.
	// StubParser.Discover returns nothing, so the old title/entries acceptance
	// clause would drop this into discovery and record "no feed found at URL".
	parser := coretest.StubParser{PF: &core.ParsedFeed{}}
	svc, _ := newFeedSvc(store, fetcher, parser)

	f, err := svc.Subscribe(ctx, core.DefaultUserID, "https://b.test/feed.xml", nil, false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, f.ID)
	if got.ErrorCount != 0 || got.LastError != "" {
		t.Fatalf("valid empty feed recorded an error: count=%d err=%q", got.ErrorCount, got.LastError)
	}
}

// upsertErrStore wraps MemStore and fails every UpsertEntries, simulating a
// store-layer ingest failure (disk full, I/O error, lock timeout).
type upsertErrStore struct{ *coretest.MemStore }

func (upsertErrStore) UpsertEntries(context.Context, core.ID, []*core.Entry) ([]*core.Entry, error) {
	return nil, errors.New("disk full")
}

// B10 #2: a feed whose stored URL serves HTML (a site-page subscribe whose initial
// discovery died) must self-heal on a later poll — parse fails, so PollFeed routes
// through the full resolve+discover path, adopts the discovered feed URL, and
// clears the stalled error state.
func TestPollFeedRediscoversHTMLFeed(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const pageURL = "https://example.com/site"
	const discoveredURL = "https://example.com/feed.xml"
	// Every fetch returns an HTML body; the parser only parses the discovered URL.
	fetcher := fixedFetcher{resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}}
	parser := discoveryParser{discoveredURL: discoveredURL, feed: &core.ParsedFeed{Title: "Blog"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	// ErrorCount > 0: an already-stuck feed (its initial resolve failed), which is
	// exactly the state the audit case reaches. Healthy feeds are covered below.
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: pageURL, Title: pageURL, ErrorCount: 1, LastError: "parse: ..."})

	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.FeedURL != discoveredURL {
		t.Fatalf("feed_url = %q, want rediscovered %q", got.FeedURL, discoveredURL)
	}
	if got.ErrorCount != 0 || got.CheckedAt == nil {
		t.Fatalf("feed still stalled after rediscovery: count=%d checkedAt=%v", got.ErrorCount, got.CheckedAt)
	}
}

// B10 review: a HEALTHY feed (ErrorCount == 0) that hits a single transient
// 200-HTML blip (a CDN/WAF interstitial) must NOT trigger rediscovery and repoint
// its URL — it just records one error and backs off. Guards against interstitial hijack.
func TestPollFeedHealthyHTMLBlipDoesNotRediscover(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const feedURL = "https://good.example/feed.xml"
	fetcher := fixedFetcher{resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>interstitial</html>")}}
	parser := discoveryParser{discoveredURL: "https://evil.example/feed.xml", feed: &core.ParsedFeed{Title: "Hijacked"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: feedURL, Title: "Good"}) // ErrorCount 0

	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.FeedURL != feedURL {
		t.Fatalf("healthy feed URL hijacked to %q on a single HTML blip", got.FeedURL)
	}
	if got.ErrorCount != 1 {
		t.Fatalf("want one recorded error, got %d", got.ErrorCount)
	}
}

// B10 #3: EditFeed applies the conflict-prone URL change first, so a duplicate-URL
// conflict leaves the other fields (title) untouched rather than half-saved.
func TestEditFeedURLConflictPersistsNothingElse(t *testing.T) {
	ctx := context.Background()
	st := coretest.NewMemStore()
	svc := core.NewFeedService(st, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{T: time.Unix(1000, 0)}, coretest.DiscardLogger(), core.FeedServiceConfig{})
	a, _ := svc.CreateSubscription(ctx, core.DefaultUserID, "https://e.com/a", nil, false)
	_, _ = svc.CreateSubscription(ctx, core.DefaultUserID, "https://e.com/b", nil, false)

	// Rename A *and* point it at B's URL: the URL step conflicts.
	_, err := svc.EditFeed(ctx, core.DefaultUserID, a.ID, core.EditFeedInput{Title: "Renamed", URL: "https://e.com/b"})
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	got, _ := st.GetFeed(ctx, core.DefaultUserID, a.ID)
	if got.UserTitle != "" {
		t.Fatalf("title persisted despite URL conflict: %q — edit was not conflict-safe", got.UserTitle)
	}
	if got.FeedURL != "https://e.com/a" {
		t.Fatalf("feed_url changed despite conflict: %q", got.FeedURL)
	}
}

// updateFeedCtxErrStore fails UpdateFeed whenever the passed ctx is already done,
// mimicking a real store rejecting a cancelled/expired context.
type updateFeedCtxErrStore struct{ *coretest.MemStore }

func (s updateFeedCtxErrStore) UpdateFeed(ctx context.Context, f *core.Feed) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemStore.UpdateFeed(ctx, f)
}

// B10 #5: when a poll fails *because* its ctx was cancelled/expired, recordError
// must still persist the error state — it detaches the ctx for the write, so the
// pending-row-becomes-error-row UX is not silently lost.
func TestRecordErrorSurvivesCancelledContext(t *testing.T) {
	store := updateFeedCtxErrStore{coretest.NewMemStore()}
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(context.Background(), &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(context.Background(), core.DefaultUserID, fid)
	fetcher := coretest.StubFetcher{Err: context.Canceled}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{PF: &core.ParsedFeed{}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the poll's ctx is already dead — the cause of the failure
	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed should swallow the error, got %v", err)
	}
	got, _ := store.GetFeed(context.Background(), core.DefaultUserID, fid)
	if got.ErrorCount != 1 || got.LastError == "" {
		t.Fatalf("error state not persisted under cancelled ctx: count=%d err=%q", got.ErrorCount, got.LastError)
	}
}

// countingSanitizer counts Sanitize calls so a test can prove the poll pipeline
// sanitises only genuinely new or hash-changed entries (F1).
type countingSanitizer struct{ n int }

func (s *countingSanitizer) Sanitize(html, _ string) string { s.n++; return html }

func TestPollDefersSanitizeToNewOrChangedEntries(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	pe := core.ParsedEntry{GUID: "g1", URL: "https://f.test/1", Title: "T", Content: "<p>c</p>", Summary: "<p>s</p>", Hash: "h1"}
	pf := &core.ParsedFeed{Title: "B", Entries: []core.ParsedEntry{pe}}
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: pf}
	san := &countingSanitizer{}
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	cfg := core.FeedServiceConfig{
		Schedule:   core.ScheduleConfig{MinInterval: 15 * time.Minute, MaxInterval: 24 * time.Hour, Factor: 1},
		Reschedule: core.RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour},
		Jitter:     func(time.Duration) time.Duration { return 0 },
	}
	svc := core.NewFeedService(store, fetcher, parser, san, clk, coretest.DiscardLogger(), cfg)
	fid := seedFeed(t, store, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://f.test/x", Title: "B"})
	poll := func() {
		f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
		if err := svc.PollFeed(ctx, f); err != nil {
			t.Fatalf("PollFeed: %v", err)
		}
	}

	// New entry: sanitise Content + Summary = 2 calls.
	poll()
	if san.n != 2 {
		t.Fatalf("first poll sanitise calls = %d, want 2", san.n)
	}

	// Unchanged (same hash): sanitise nothing.
	san.n = 0
	poll()
	if san.n != 0 {
		t.Fatalf("unchanged entry re-sanitised %d times, want 0", san.n)
	}

	// Changed hash: sanitise Content + Summary again.
	san.n = 0
	pf.Entries[0].Hash = "h2"
	poll()
	if san.n != 2 {
		t.Fatalf("changed entry sanitise calls = %d, want 2", san.n)
	}

	// Tombstoned (deleted) guid reappears: sanitise nothing (dropped pre-sanitise).
	list, _, _ := store.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Limit: 10})
	if len(list) != 1 {
		t.Fatalf("want 1 entry, got %d", len(list))
	}
	if err := store.DeleteEntry(ctx, core.DefaultUserID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	san.n = 0
	poll()
	if san.n != 0 {
		t.Fatalf("tombstoned guid re-sanitised %d times, want 0", san.n)
	}
}
