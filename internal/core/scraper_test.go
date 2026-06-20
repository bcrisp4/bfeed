package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// recordingScraper records every entry id it is asked to scrape.
type recordingScraper struct {
	mu   sync.Mutex
	seen map[core.ID]bool
}

func (r *recordingScraper) ScrapeEntry(_ context.Context, e *core.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[e.ID] = true
	return nil
}

func TestScraperDispatchesPendingEntries(t *testing.T) {
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", PublishedAt: clk.T, CreatedAt: clk.T, ExtractState: core.ExtractPending})
	rec := &recordingScraper{seen: map[core.ID]bool{}}
	sc := core.NewScraper(store, rec, clk, coretest.DiscardLogger(), core.ScraperConfig{Tick: time.Hour, Batch: 10, Workers: 2})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sc.Run(ctx); close(done) }()
	// Run dispatches once immediately before the first tick; poll briefly for the effect.
	deadline := time.Now().Add(2 * time.Second)
	seen := func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.seen[id]
	}
	for !seen() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if !seen() {
		t.Fatalf("scraper did not dispatch pending entry %d", id)
	}
}
