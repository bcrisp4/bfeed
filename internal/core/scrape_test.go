package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func newScrapeFixture(t *testing.T, fetch core.Fetcher, ext core.Extractor) (*core.ScrapeService, *coretest.MemStore, *coretest.StubClock) {
	t.Helper()
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	svc := core.NewScrapeService(store, fetch, ext, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	return svc, store, clk
}

func TestScrapeEntrySuccessWritesContentAndMarksDone(t *testing.T) {
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html; charset=utf-8", Body: []byte("<html>..</html>")}}
	ext := coretest.StubExtractor{HTML: "<p>extracted</p>"}
	svc, store, _ := newScrapeFixture(t, fetch, ext)
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	if err := svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending}); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.Content != "<p>extracted</p>" || got.ExtractState != core.ExtractDone {
		t.Fatalf("got %q %q", got.Content, got.ExtractState)
	}
}

func TestScrapeEntryRetriesThenFails(t *testing.T) {
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 500}}
	svc, store, _ := newScrapeFixture(t, fetch, coretest.StubExtractor{})
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	e := &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 0}
	// attempt 1 → still pending, attempt count 1
	_ = svc.ScrapeEntry(context.Background(), e)
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.ExtractState != core.ExtractPending || got.ExtractAttempts != 1 {
		t.Fatalf("after 1 fail: %q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
	// drive to the cap
	_ = svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 1})
	_ = svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 2})
	got, _ = store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.ExtractState != core.ExtractFailed {
		t.Fatalf("want failed at cap, got %q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
}

func TestExtractBackoffGrowsAndCaps(t *testing.T) {
	cfg := core.ScrapeConfig{BaseBackoff: 10 * time.Minute, MaxBackoff: time.Hour}
	b1 := core.ExtractBackoff(cfg, 1, nil)
	b2 := core.ExtractBackoff(cfg, 2, nil)
	b9 := core.ExtractBackoff(cfg, 9, nil)
	if b1 != 10*time.Minute || b2 != 20*time.Minute || b9 != time.Hour {
		t.Fatalf("backoff: %v %v %v", b1, b2, b9)
	}
}
