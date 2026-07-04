package coretest_test

import (
	"sync"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// RecordingMetrics must satisfy core.Metrics and faithfully record every call
// in order, including under concurrent use (services call it from worker
// goroutines), so tests built on it aren't themselves racy.
func TestRecordingMetricsRecordsCallsConcurrently(t *testing.T) {
	m := &coretest.RecordingMetrics{}

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			m.FeedPollDone(core.PollSuccess)
			m.ObserveFeedPoll(time.Millisecond)
			m.ScrapeDone(core.ScrapeFailed)
			m.ObserveArticleScrape(2 * time.Millisecond)
			m.ErrorObserved(core.CompFeedPoll, core.ReasonTimeout)
			m.AddPollInflight(1)
			m.AddScrapeInflight(1)
			m.PollerTicked(time.Unix(1, 0))
			m.ScraperTicked(time.Unix(2, 0))
		}()
	}
	wg.Wait()

	if got := len(m.SnapshotPollResults()); got != n {
		t.Fatalf("poll results = %d, want %d", got, n)
	}
	if got := len(m.SnapshotScrapeResults()); got != n {
		t.Fatalf("scrape results = %d, want %d", got, n)
	}
	if got := len(m.SnapshotErrors()); got != n {
		t.Fatalf("errors = %d, want %d", got, n)
	}
	if got := len(m.SnapshotPollDurations()); got != n {
		t.Fatalf("poll durations = %d, want %d", got, n)
	}
	if got := len(m.SnapshotScrapeDurations()); got != n {
		t.Fatalf("scrape durations = %d, want %d", got, n)
	}
	if got := m.PollInflight(); got != n {
		t.Fatalf("poll inflight = %d, want %d", got, n)
	}
	if got := m.ScrapeInflight(); got != n {
		t.Fatalf("scrape inflight = %d, want %d", got, n)
	}
	if got := len(m.SnapshotPollerTicks()); got != n {
		t.Fatalf("poller ticks = %d, want %d", got, n)
	}
	if got := len(m.SnapshotScraperTicks()); got != n {
		t.Fatalf("scraper ticks = %d, want %d", got, n)
	}
}

// AddPollInflight/AddScrapeInflight must accumulate deltas (including negative
// ones), not just count calls — services call Add*Inflight(-1) when work finishes.
func TestRecordingMetricsInflightAccumulatesDeltas(t *testing.T) {
	m := &coretest.RecordingMetrics{}

	m.AddPollInflight(3)
	m.AddPollInflight(-1)
	if got := m.PollInflight(); got != 2 {
		t.Fatalf("poll inflight = %d, want 2", got)
	}

	m.AddScrapeInflight(5)
	m.AddScrapeInflight(-5)
	if got := m.ScrapeInflight(); got != 0 {
		t.Fatalf("scrape inflight = %d, want 0", got)
	}
}
