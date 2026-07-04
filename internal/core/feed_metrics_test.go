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
		{
			// F2/F9: a non-200 status outside both the 4xx and 5xx ranges (e.g. a
			// stray 204) is not an HTTP client/server failure in the usual sense —
			// classifyHTTPStatus buckets it to internal rather than the
			// misleading http_4xx the old "else -> 4xx" ladder gave it.
			name:       "204 is an unexpected non-error status -> internal, not http_4xx",
			fetcher:    coretest.StubFetcher{Resp: &core.FetchResponse{Status: 204}},
			parser:     coretest.StubParser{},
			wantResult: core.PollHTTPError,
			wantReason: reasonPtr(core.ReasonInternal),
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

// F1(a): the direct ResolveAndIngest path (used by subscribe) must emit
// exactly one FeedPollDone AND exactly one ObserveFeedPoll per call —
// previously it only emitted the counter (via recordSuccess/recordError),
// with no matching duration sample.
func TestResolveAndIngestDirectPathEmitsMatchedCounterAndDuration(t *testing.T) {
	store := coretest.NewMemStore()
	f := seedPollFeed(t, store)
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.ResolveAndIngest(context.Background(), f); err != nil {
		t.Fatalf("ResolveAndIngest: %v", err)
	}

	if results := m.SnapshotPollResults(); len(results) != 1 || results[0] != core.PollSuccess {
		t.Fatalf("FeedPollDone results = %v, want exactly [success]", results)
	}
	if durs := m.SnapshotPollDurations(); len(durs) != 1 {
		t.Fatalf("ObserveFeedPoll called %d times, want exactly 1", len(durs))
	}
}

// F1(a): Subscribe (CreateSubscription + ResolveAndIngest) must also produce
// a matched counter/duration pair for the resolve it triggers.
func TestSubscribeEmitsMatchedCounterAndDuration(t *testing.T) {
	store := coretest.NewMemStore()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>")}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog"}}
	svc, _ := newFeedSvc(store, fetcher, parser)
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if _, err := svc.Subscribe(context.Background(), core.DefaultUserID, "https://b.test/feed.xml", nil, false); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if results := m.SnapshotPollResults(); len(results) != 1 || results[0] != core.PollSuccess {
		t.Fatalf("FeedPollDone results = %v, want exactly [success]", results)
	}
	if durs := m.SnapshotPollDurations(); len(durs) != 1 {
		t.Fatalf("ObserveFeedPoll called %d times, want exactly 1", len(durs))
	}
}

// F1(b) DECIDED: the self-heal dup-delete branch (resolveAndIngest's
// SetFeedURL hits ErrConflict, the duplicate row is deleted, and
// resolveAndIngest returns nil with no recordSuccess/recordError call) must
// still emit a matching FeedPollDone(success) — otherwise PollFeed's own
// deferred ObserveFeedPoll fires with no paired counter increment for this
// attempt.
func TestPollFeedSelfHealDupDeleteEmitsMatchedSuccess(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	const pageURL = "https://example.com/site"
	const discoveredURL = "https://example.com/feed.xml"
	// Existing row already owns the discovered URL, so self-heal's SetFeedURL
	// (on the duplicate row created below) will conflict.
	if _, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: discoveredURL, Title: "Existing"}); err != nil {
		t.Fatalf("seed existing feed: %v", err)
	}
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

	if _, err := store.GetFeed(ctx, core.DefaultUserID, fid); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("duplicate row must be deleted on conflict, got err=%v", err)
	}
	results := m.SnapshotPollResults()
	if len(results) != 1 || results[0] != core.PollSuccess {
		t.Fatalf("FeedPollDone results = %v, want exactly [success] (F1 decided: dup-delete emits success)", results)
	}
	if durs := m.SnapshotPollDurations(); len(durs) != 1 {
		t.Fatalf("ObserveFeedPoll durations = %v, want exactly 1 entry (PollFeed's own defer)", durs)
	}
}

// F3: a fetch that fails because the poll's own ctx was cancelled (shutdown)
// must not pollute the counters — it isn't a poll failure worth counting.
func TestPollFeedShutdownCancelSkipsMetricsEmission(t *testing.T) {
	store := coretest.NewMemStore()
	f := seedPollFeed(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate shutdown: ctx is already done when Fetch is attempted
	fetcher := coretest.StubFetcher{Err: context.Canceled}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{})
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.PollFeed(ctx, f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	if results := m.SnapshotPollResults(); len(results) != 0 {
		t.Fatalf("FeedPollDone = %v, want none (shutdown cancel must not emit)", results)
	}
	if errs := m.SnapshotErrors(); len(errs) != 0 {
		t.Fatalf("ErrorObserved = %v, want none (shutdown cancel must not emit)", errs)
	}
	// The error must still be persisted (existing recordError semantics
	// unaffected — metrics-only suppression).
	got, _ := store.GetFeed(context.Background(), core.DefaultUserID, f.ID)
	if got.ErrorCount == 0 {
		t.Fatalf("ErrorCount = 0, want > 0 (error must still persist even though metrics are suppressed)")
	}
}

// F3: a normal (non-shutdown) fetch error must be unaffected — guards against
// an overly broad ctxShutdownCanceled check skipping emission for unrelated
// context.Canceled errors.
func TestPollFeedNormalCancelStillEmitsMetrics(t *testing.T) {
	store := coretest.NewMemStore()
	f := seedPollFeed(t, store)
	// ctx itself is live (not cancelled); the fetcher's error is context.Canceled
	// from some unrelated inner operation, not the poll's own shutdown.
	fetcher := coretest.StubFetcher{Err: context.Canceled}
	svc, _ := newFeedSvc(store, fetcher, coretest.StubParser{})
	m := &coretest.RecordingMetrics{}
	svc.SetMetrics(m)

	if err := svc.PollFeed(context.Background(), f); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	if results := m.SnapshotPollResults(); len(results) != 1 || results[0] != core.PollFetchError {
		t.Fatalf("FeedPollDone = %v, want exactly [fetch_error]", results)
	}
	if errs := m.SnapshotErrors(); len(errs) != 1 {
		t.Fatalf("ErrorObserved = %v, want exactly one entry", errs)
	}
}
