package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// errPendingLister always fails ListPendingExtractions, simulating a
// store-layer failure at dispatch time.
type errPendingLister struct{}

func (errPendingLister) ListPendingExtractions(context.Context, time.Time, int) ([]*core.Entry, error) {
	return nil, errors.New("boom")
}

// TestScraperTickedEmittedEvenOnStoreError proves liveness (ScraperTicked)
// does not stall on a ListPendingExtractions failure — the tick must be
// recorded before the store call, and the failure itself must surface as
// ErrorObserved(article_scrape, internal).
func TestScraperTickedEmittedEvenOnStoreError(t *testing.T) {
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	sc := core.NewScraper(errPendingLister{}, noopScraper{}, clk, coretest.DiscardLogger(), core.ScraperConfig{Tick: time.Hour, Batch: 10, Workers: 1})
	m := &coretest.RecordingMetrics{}
	sc.SetMetrics(m)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sc.Run(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for len(m.SnapshotScraperTicks()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	ticks := m.SnapshotScraperTicks()
	if len(ticks) != 1 {
		t.Fatalf("ScraperTicked called %d times, want exactly 1 (initial dispatch)", len(ticks))
	}
	if !ticks[0].Equal(clk.T) {
		t.Fatalf("ScraperTicked time = %v, want %v (from clk.Now())", ticks[0], clk.T)
	}

	errs := m.SnapshotErrors()
	want := coretest.RecordedError{C: core.CompArticleScrape, R: core.ReasonInternal}
	if len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", errs, want)
	}
}

// noopScraper is an EntryScraper that is never expected to be invoked in the
// store-error test above (dispatch returns before any job is queued).
type noopScraper struct{}

func (noopScraper) ScrapeEntry(context.Context, *core.Entry) error { return nil }

// TestScraperInflightTracksBlockingScrape asserts AddScrapeInflight(1) is
// emitted before a scrape starts and AddScrapeInflight(-1) after it finishes
// (via the defer ordered after RecoverGuard, so a panicking scrape would
// still decrement) — proven here with a scrape that blocks on a channel
// rather than panicking, since blocking lets the test observe the inflight
// count while the scrape is still in progress.
func TestScraperInflightTracksBlockingScrape(t *testing.T) {
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", PublishedAt: now, CreatedAt: now, ExtractState: core.ExtractPending})
	started := make(chan struct{})
	release := make(chan struct{})
	bs := &blockingScraper{started: started, release: release}
	clk := coretest.StubClock{T: now}
	sc := core.NewScraper(store, bs, clk, coretest.DiscardLogger(), core.ScraperConfig{Tick: time.Hour, Batch: 10, Workers: 1})
	m := &coretest.RecordingMetrics{}
	sc.SetMetrics(m)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sc.Run(ctx); close(done) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("scrape never started")
	}

	deadline := time.Now().Add(2 * time.Second)
	for m.ScrapeInflight() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.ScrapeInflight(); got != 1 {
		t.Fatalf("ScrapeInflight = %d while scrape blocked, want 1", got)
	}

	close(release)

	deadline = time.Now().Add(2 * time.Second)
	for m.ScrapeInflight() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.ScrapeInflight(); got != 0 {
		t.Fatalf("ScrapeInflight = %d after scrape released, want 0", got)
	}

	cancel()
	<-done
}

// blockingScraper signals started (once) then blocks until release closes.
type blockingScraper struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (s *blockingScraper) ScrapeEntry(ctx context.Context, _ *core.Entry) error {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return nil
}
