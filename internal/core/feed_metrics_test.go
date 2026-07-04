package core_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// errParser always fails to parse, simulating a body that is neither a
// recognisable feed nor HTML — so PollFeed's self-heal rediscovery gate
// (isHTML(contentType) && ErrorCount > 0) never engages, regardless of the
// feed's ErrorCount, keeping the parse-error path isolated for assertions.
type errParser struct{ err error }

func (p errParser) Parse([]byte, string, string) (*core.ParsedFeed, error) { return nil, p.err }
func (p errParser) Discover([]byte, string) ([]string, error)              { return nil, nil }

// seedPollFeed creates and returns a feed row ready for PollFeed.
func seedPollFeed(t *testing.T, store *coretest.MemStore) *core.Feed {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	fid, err := store.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://b.test/feed.xml",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	f, err := store.GetFeed(ctx, core.DefaultUserID, fid)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	return f
}

// TestPollFeedEmitsMetricsPerResult tables every fetcher/parser outcome PollFeed
// can hit and asserts the exact (FeedPollDone result, ErrorObserved reason) pair
// emitted, and that FeedPollDone fires exactly once per PollFeed call (the
// invariant issue #65 depends on for correct Prometheus counters).
func TestPollFeedEmitsMetricsPerResult(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "dead.example", IsNotFound: true}

	tests := []struct {
		name       string
		fetcher    core.Fetcher
		parser     core.FeedParser
		wantResult core.PollResult
		wantReason *core.ErrorReason // nil means no ErrorObserved call expected
	}{
		{
			name:       "200 + parse ok is success",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}},
			parser:     coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog"}},
			wantResult: core.PollSuccess,
		},
		{
			name:       "304 is not_modified",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{NotModified: true}},
			parser:     coretest.StubParser{},
			wantResult: core.PollNotModified,
		},
		{
			name:       "fetch error classifies via ClassifyFetchError",
			fetcher:    coretest.StubFetcher{Err: dnsErr},
			parser:     coretest.StubParser{},
			wantResult: core.PollFetchError,
			wantReason: reasonPtr(core.ReasonDNS),
		},
		{
			name:       "429 is rate limited (takes precedence over generic http_error)",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 429}},
			parser:     coretest.StubParser{},
			wantResult: core.PollHTTPError,
			wantReason: reasonPtr(core.ReasonRateLimited),
		},
		{
			name:       "500 is http_5xx",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 500}},
			parser:     coretest.StubParser{},
			wantResult: core.PollHTTPError,
			wantReason: reasonPtr(core.ReasonHTTP5xx),
		},
		{
			name:       "404 is http_4xx",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 404}},
			parser:     coretest.StubParser{},
			wantResult: core.PollHTTPError,
			wantReason: reasonPtr(core.ReasonHTTP4xx),
		},
		{
			name:       "200 + parse error (non-HTML) is parse_error",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "application/xml", Body: []byte("<bogus/>")}},
			parser:     errParser{err: errors.New("boom")},
			wantResult: core.PollParseError,
			wantReason: reasonPtr(core.ReasonParse),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := coretest.NewMemStore()
			f := seedPollFeed(t, store)
			svc, _ := newFeedSvc(store, tt.fetcher, tt.parser)
			m := &coretest.RecordingMetrics{}
			svc.SetMetrics(m)

			if err := svc.PollFeed(context.Background(), f); err != nil {
				t.Fatalf("PollFeed returned error (errors must be swallowed): %v", err)
			}

			results := m.SnapshotPollResults()
			if len(results) != 1 || results[0] != tt.wantResult {
				t.Fatalf("FeedPollDone results = %v, want exactly [%v]", results, tt.wantResult)
			}

			errs := m.SnapshotErrors()
			if tt.wantReason == nil {
				if len(errs) != 0 {
					t.Fatalf("ErrorObserved = %v, want none", errs)
				}
			} else {
				want := coretest.RecordedError{C: core.CompFeedPoll, R: *tt.wantReason}
				if len(errs) != 1 || errs[0] != want {
					t.Fatalf("ErrorObserved = %v, want exactly [%v]", errs, want)
				}
			}

			if durs := m.SnapshotPollDurations(); len(durs) != 1 {
				t.Fatalf("ObserveFeedPoll called %d times, want exactly 1", len(durs))
			}
		})
	}
}

// A panic while parsing must still emit exactly one FeedPollDone (panic) plus one
// ErrorObserved(feed_poll, internal) — recordPanic delegates entirely to
// recordError, so it must not double-count on top of recordError's own emission.
func TestPollFeedParserPanicEmitsExactlyOnePanicResult(t *testing.T) {
	store := coretest.NewMemStore()
	f := seedPollFeed(t, store)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	svc, _ := newFeedSvc(store, fetcher, panicParser{})
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.PollFeed(context.Background(), f); err != nil {
		t.Fatalf("PollFeed must recover the panic, got error: %v", err)
	}

	results := m.SnapshotPollResults()
	if len(results) != 1 || results[0] != core.PollPanic {
		t.Fatalf("FeedPollDone results = %v, want exactly [panic]", results)
	}
	errs := m.SnapshotErrors()
	want := coretest.RecordedError{C: core.CompFeedPoll, R: core.ReasonInternal}
	if len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", errs, want)
	}
	if durs := m.SnapshotPollDurations(); len(durs) != 1 {
		t.Fatalf("ObserveFeedPoll called %d times, want exactly 1", len(durs))
	}
}

// A store-layer failure during ingest (recordSuccess's ingest call) must be
// reported as store_error/internal, and recordSuccess must not also emit a
// success result on top of recordError's emission (exactly one FeedPollDone).
func TestPollFeedIngestStoreFailureEmitsStoreError(t *testing.T) {
	store := upsertErrStore{coretest.NewMemStore()}
	f := seedPollFeed(t, store.MemStore)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog", Entries: []core.ParsedEntry{{GUID: "g1", URL: "https://b.test/1"}}}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.PollFeed(context.Background(), f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	results := m.SnapshotPollResults()
	if len(results) != 1 || results[0] != core.PollStoreError {
		t.Fatalf("FeedPollDone results = %v, want exactly [store_error]", results)
	}
	errs := m.SnapshotErrors()
	want := coretest.RecordedError{C: core.CompFeedPoll, R: core.ReasonInternal}
	if len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", errs, want)
	}
}

// tickClock returns a monotonically increasing sequence of timestamps, advancing
// by step on every Now() call. Local to this file only: it exists to prove
// ObserveFeedPoll's recorded duration reflects real elapsed time between
// PollFeed's entry and its (deferred) exit, not merely that a fixed clock always
// yields a zero duration.
type tickClock struct {
	mu   sync.Mutex
	next time.Time
	step time.Duration
}

func (c *tickClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.next
	c.next = c.next.Add(c.step)
	return t
}

func TestPollFeedObservesDurationExactlyOnce(t *testing.T) {
	store := coretest.NewMemStore()
	f := seedPollFeed(t, store)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog"}}
	clk := &tickClock{next: time.Unix(1_700_000_000, 0).UTC(), step: 250 * time.Millisecond}
	cfg := core.FeedServiceConfig{
		Schedule:   core.ScheduleConfig{MinInterval: 15 * time.Minute, MaxInterval: 24 * time.Hour, Factor: 1},
		Reschedule: core.RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour},
		Jitter:     func(time.Duration) time.Duration { return 0 },
	}
	svc := core.NewFeedService(store, fetcher, parser, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(), cfg)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.PollFeed(context.Background(), f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	durs := m.SnapshotPollDurations()
	if len(durs) != 1 {
		t.Fatalf("ObserveFeedPoll called %d times, want exactly 1", len(durs))
	}
	if durs[0] <= 0 {
		t.Fatalf("duration = %v, want > 0 (clock advanced during the call)", durs[0])
	}
}

// PollFeed's HTML self-heal branch (audit B10) delegates entirely to
// ResolveAndIngest; the outer PollFeed must not also emit a FeedPollDone after
// the delegate returns, or a self-healed poll would be double-counted.
func TestPollFeedSelfHealDelegatesMetricsWithoutDoubleCounting(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const pageURL = "https://example.com/site"
	const discoveredURL = "https://example.com/feed.xml"
	fetcher := fixedFetcher{resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>")}}
	parser := discoveryParser{discoveredURL: discoveredURL, feed: &core.ParsedFeed{Title: "Blog"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	// ErrorCount > 0 is required to enter the self-heal branch (see feed.go).
	fid, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: pageURL, Title: pageURL, ErrorCount: 1, LastError: "parse: ..."})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	results := m.SnapshotPollResults()
	if len(results) != 1 || results[0] != core.PollSuccess {
		t.Fatalf("FeedPollDone results = %v, want exactly [success] (self-heal delegate's own emission, no double-count from the outer PollFeed)", results)
	}
	if durs := m.SnapshotPollDurations(); len(durs) != 1 {
		t.Fatalf("ObserveFeedPoll durations = %v, want exactly 1 entry (only PollFeed's own defer, not ResolveAndIngest)", durs)
	}
}

func reasonPtr(r core.ErrorReason) *core.ErrorReason { return &r }
