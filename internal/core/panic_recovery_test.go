package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// panicParser panics on Parse, simulating a pathological feed body that trips a
// bug in the parser on attacker-controlled XML.
type panicParser struct{}

func (panicParser) Parse([]byte, string) (*core.ParsedFeed, error) { panic("parser blew up") }
func (panicParser) Discover([]byte, string) ([]string, error)      { panic("discover blew up") }

// panicExtractor panics on Extract, simulating readability choking on a
// pathological article HTML.
type panicExtractor struct{}

func (panicExtractor) Extract(context.Context, string, []byte) (string, error) {
	panic("extractor blew up")
}

// A panic while parsing untrusted feed content must not escape PollFeed: it is
// recovered and recorded as a feed error so the feed reschedules (backs off)
// instead of killing the process and staying due forever.
func TestPollFeedRecoversPanic(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/feed.xml", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)

	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	svc, _ := newFeedSvc(store, fetcher, panicParser{})

	err := svc.PollFeed(ctx, f) // must not panic
	if err != nil {
		t.Fatalf("PollFeed returned error after recovering panic: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.ErrorCount != 1 || got.LastError == "" {
		t.Fatalf("panic not recorded as feed error: ErrorCount=%d LastError=%q", got.ErrorCount, got.LastError)
	}
	// Rescheduled into the future so the poller does not immediately re-dispatch
	// the same panic-triggering input (no crash/busy loop).
	if !got.NextCheckAt.After(now) {
		t.Fatalf("feed not rescheduled after panic: NextCheckAt=%v now=%v", got.NextCheckAt, now)
	}
}

// A panic during subscribe-time resolve must be recovered and recorded, leaving
// the feed row in an error state rather than crashing the background goroutine.
func TestResolveAndIngestRecoversPanic(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/feed.xml", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)

	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	svc, _ := newFeedSvc(store, fetcher, panicParser{})

	if err := svc.ResolveAndIngest(ctx, f); err != nil {
		t.Fatalf("ResolveAndIngest returned error after recovering panic: %v", err)
	}
	got, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if got.ErrorCount != 1 || got.LastError == "" {
		t.Fatalf("panic not recorded as feed error: ErrorCount=%d LastError=%q", got.ErrorCount, got.LastError)
	}
}

// A panic while extracting untrusted article HTML must not escape ScrapeEntry:
// it is recovered and recorded as an extraction failure so the entry reschedules
// instead of killing the process.
func TestScrapeEntryRecoversPanic(t *testing.T) {
	ctx := context.Background()
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html></html>")}}
	svc, store, _ := newScrapeFixture(t, fetch, panicExtractor{})
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})

	err := svc.ScrapeEntry(ctx, &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending}) // must not panic
	if err != nil {
		t.Fatalf("ScrapeEntry returned error after recovering panic: %v", err)
	}
	got, _ := store.GetEntry(ctx, core.DefaultUserID, id)
	if got.ExtractAttempts != 1 || got.ExtractState != core.ExtractPending {
		t.Fatalf("panic not recorded as extraction failure: state=%q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
}
