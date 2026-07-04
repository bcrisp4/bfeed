package core_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// panicExtractor (shared with panic_recovery_test.go) panics on Extract.

// seedScrapeEntry creates and returns an entry ready for ScrapeEntry, plus its ID.
func seedScrapeEntry(t *testing.T, store *coretest.MemStore) *core.Entry {
	t.Helper()
	id := coretest.SeedEntry(store, &core.Entry{
		UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending,
	})
	return &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending}
}

// TestScrapeEntryEmitsMetricsPerResult tables every fetcher/extractor outcome
// ScrapeEntry can hit and asserts the exact (ScrapeDone result, ErrorObserved
// reason) pair emitted, and that ScrapeDone fires exactly once per ScrapeEntry
// call (the invariant issue #65 depends on for correct Prometheus counters).
func TestScrapeEntryEmitsMetricsPerResult(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "dead.example", IsNotFound: true}

	tests := []struct {
		name       string
		fetcher    core.Fetcher
		ext        core.Extractor
		wantResult core.ScrapeResult
		wantReason *core.ErrorReason // nil means no ErrorObserved call expected
	}{
		{
			name:       "200 html + extract ok is success",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html; charset=utf-8", Body: []byte("<html>..</html>")}},
			ext:        coretest.StubExtractor{HTML: "<p>extracted</p>"},
			wantResult: core.ScrapeSuccess,
		},
		{
			name:       "fetch error classifies via ClassifyFetchError",
			fetcher:    coretest.StubFetcher{Err: dnsErr},
			ext:        coretest.StubExtractor{},
			wantResult: core.ScrapeFetchError,
			wantReason: reasonPtr(core.ReasonDNS),
		},
		{
			name:       "429 is retried + rate limited",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 429, ContentType: "text/html"}},
			ext:        coretest.StubExtractor{},
			wantResult: core.ScrapeRetried,
			wantReason: reasonPtr(core.ReasonRateLimited),
		},
		{
			name:       "500 is retried + http_5xx",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 500, ContentType: "text/html"}},
			ext:        coretest.StubExtractor{},
			wantResult: core.ScrapeRetried,
			wantReason: reasonPtr(core.ReasonHTTP5xx),
		},
		{
			name:       "404 is http_error + http_4xx (burns an attempt)",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 404, ContentType: "text/html"}},
			ext:        coretest.StubExtractor{},
			wantResult: core.ScrapeHTTPError,
			wantReason: reasonPtr(core.ReasonHTTP4xx),
		},
		{
			name:       "extract error is extract_error + parse",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}},
			ext:        coretest.StubExtractor{Err: errors.New("boom")},
			wantResult: core.ScrapeExtractError,
			wantReason: reasonPtr(core.ReasonParse),
		},
		{
			name:       "empty extracted content is extract_error + parse",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}},
			ext:        coretest.StubExtractor{HTML: "   "},
			wantResult: core.ScrapeExtractError,
			wantReason: reasonPtr(core.ReasonParse),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := coretest.NewMemStore()
			e := seedScrapeEntry(t, store)
			clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
			svc := core.NewScrapeService(store, tt.fetcher, tt.ext, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
				core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
			m := &coretest.RecordingMetrics{}
			svc.SetMetrics(m)

			if err := svc.ScrapeEntry(context.Background(), e); err != nil {
				t.Fatalf("ScrapeEntry returned error (errors must be swallowed): %v", err)
			}

			results := m.SnapshotScrapeResults()
			if len(results) != 1 || results[0] != tt.wantResult {
				t.Fatalf("ScrapeDone results = %v, want exactly [%v]", results, tt.wantResult)
			}

			errs := m.SnapshotErrors()
			if tt.wantReason == nil {
				if len(errs) != 0 {
					t.Fatalf("ErrorObserved = %v, want none", errs)
				}
			} else {
				want := coretest.RecordedError{C: core.CompArticleScrape, R: *tt.wantReason}
				if len(errs) != 1 || errs[0] != want {
					t.Fatalf("ErrorObserved = %v, want exactly [%v]", errs, want)
				}
			}

			if durs := m.SnapshotScrapeDurations(); len(durs) != 1 {
				t.Fatalf("ObserveArticleScrape called %d times, want exactly 1", len(durs))
			}
		})
	}
}

// A sanitiser that strips everything down to whitespace simulates extraction
// yielding only content bluemonday removes entirely (e.g. a script-only body).
type blankSanitizer struct{}

func (blankSanitizer) Sanitize(string, string) string { return "   " }

func TestScrapeEntrySanitisedEmptyIsExtractError(t *testing.T) {
	store := coretest.NewMemStore()
	e := seedScrapeEntry(t, store)
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}}
	ext := coretest.StubExtractor{HTML: "<script>evil()</script>"}
	svc := core.NewScrapeService(store, fetch, ext, blankSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.ScrapeEntry(context.Background(), e); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}

	results := m.SnapshotScrapeResults()
	if len(results) != 1 || results[0] != core.ScrapeExtractError {
		t.Fatalf("ScrapeDone results = %v, want exactly [extract_error]", results)
	}
	want := coretest.RecordedError{C: core.CompArticleScrape, R: core.ReasonParse}
	if errs := m.SnapshotErrors(); len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", m.SnapshotErrors(), want)
	}
}

// setEntryContentErrStore2 wraps MemStore and fails every SetEntryContent write,
// simulating a store-layer persist failure during extraction (mirrors
// setEntryContentErrStore in scrape_test.go, renamed to avoid a duplicate
// declaration across files in the same package).
type setEntryContentErrStore2 struct{ *coretest.MemStore }

func (setEntryContentErrStore2) SetEntryContent(context.Context, core.ID, string) error {
	return errors.New("disk full")
}

func TestScrapeEntryPersistFailureIsFailedInternal(t *testing.T) {
	inner := coretest.NewMemStore()
	store := setEntryContentErrStore2{inner}
	e := seedScrapeEntry(t, inner)
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}}
	ext := coretest.StubExtractor{HTML: "<p>extracted</p>"}
	svc := core.NewScrapeService(store, fetch, ext, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.ScrapeEntry(context.Background(), e); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}

	results := m.SnapshotScrapeResults()
	if len(results) != 1 || results[0] != core.ScrapeFailed {
		t.Fatalf("ScrapeDone results = %v, want exactly [failed]", results)
	}
	want := coretest.RecordedError{C: core.CompArticleScrape, R: core.ReasonInternal}
	if errs := m.SnapshotErrors(); len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", m.SnapshotErrors(), want)
	}
}

// A panic while extracting must still emit exactly one ScrapeDone (failed) plus
// one ErrorObserved(article_scrape, internal) — the recover path routes through
// fail() same as any other burns-an-attempt failure.
func TestScrapeEntryPanicEmitsExactlyOneFailedResult(t *testing.T) {
	store := coretest.NewMemStore()
	e := seedScrapeEntry(t, store)
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}}
	svc := core.NewScrapeService(store, fetch, panicExtractor{}, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.ScrapeEntry(context.Background(), e); err != nil {
		t.Fatalf("ScrapeEntry must recover the panic, got error: %v", err)
	}

	results := m.SnapshotScrapeResults()
	if len(results) != 1 || results[0] != core.ScrapeFailed {
		t.Fatalf("ScrapeDone results = %v, want exactly [failed]", results)
	}
	want := coretest.RecordedError{C: core.CompArticleScrape, R: core.ReasonInternal}
	if errs := m.SnapshotErrors(); len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", m.SnapshotErrors(), want)
	}
	if durs := m.SnapshotScrapeDurations(); len(durs) != 1 {
		t.Fatalf("ObserveArticleScrape called %d times, want exactly 1", len(durs))
	}
}

// tickClockScrape returns a monotonically increasing sequence of timestamps,
// advancing by step on every Now() call — proves ObserveArticleScrape's
// recorded duration reflects real elapsed time, not merely that a fixed clock
// always yields a zero duration (mirrors tickClock in feed_metrics_test.go,
// renamed to avoid a duplicate declaration in the same package).
type tickClockScrape struct {
	next time.Time
	step time.Duration
}

func (c *tickClockScrape) Now() time.Time {
	t := c.next
	c.next = c.next.Add(c.step)
	return t
}

func TestScrapeEntryObservesDurationExactlyOnce(t *testing.T) {
	store := coretest.NewMemStore()
	e := seedScrapeEntry(t, store)
	clk := &tickClockScrape{next: time.Unix(1_700_000_000, 0).UTC(), step: 250 * time.Millisecond}
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}}
	ext := coretest.StubExtractor{HTML: "<p>extracted</p>"}
	svc := core.NewScrapeService(store, fetch, ext, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.ScrapeEntry(context.Background(), e); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}

	durs := m.SnapshotScrapeDurations()
	if len(durs) != 1 {
		t.Fatalf("ObserveArticleScrape called %d times, want exactly 1", len(durs))
	}
	if durs[0] <= 0 {
		t.Fatalf("duration = %v, want > 0 (clock advanced during the call)", durs[0])
	}
}

// TestScrapeEntryRetriedThenTerminalStillOneResultPerCall re-drives the same
// transient-then-permanent-failure sequence as TestScrapeEntryRetriesThenFails
// in scrape_test.go, asserting each individual ScrapeEntry call emits exactly
// one ScrapeDone — the exactly-once invariant must hold across repeated calls
// on the same entry, not just in isolation.
func TestScrapeEntryRetriedThenTerminalStillOneResultPerCall(t *testing.T) {
	store := coretest.NewMemStore()
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 404, ContentType: "text/html"}}
	svc := core.NewScrapeService(store, fetch, coretest.StubExtractor{}, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	for attempt := 0; attempt < 3; attempt++ {
		e := &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: attempt}
		if err := svc.ScrapeEntry(context.Background(), e); err != nil {
			t.Fatalf("ScrapeEntry attempt %d: %v", attempt, err)
		}
	}

	results := m.SnapshotScrapeResults()
	if len(results) != 3 {
		t.Fatalf("ScrapeDone called %d times over 3 ScrapeEntry calls, want exactly 3 (one per call)", len(results))
	}
	for i, r := range results {
		if r != core.ScrapeHTTPError {
			t.Fatalf("result[%d] = %v, want http_error", i, r)
		}
	}
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.ExtractState != core.ExtractFailed {
		t.Fatalf("want terminal failed state at cap, got %q", got.ExtractState)
	}
}
