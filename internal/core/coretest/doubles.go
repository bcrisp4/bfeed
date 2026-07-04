package coretest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// SeedEntry stages e into store verbatim (honoring its ExtractState) and returns its
// assigned ID. Unlike UpsertEntries it does not derive extract_state from the feed, so
// tests can stage arbitrary entry state without first configuring a feed.
func SeedEntry(store *MemStore, e *core.Entry) core.ID {
	return store.SeedEntry(e)
}

type StubFetcher struct {
	Resp *core.FetchResponse
	Err  error
}

func (f StubFetcher) Fetch(context.Context, core.FetchRequest) (*core.FetchResponse, error) {
	return f.Resp, f.Err
}

// StubStreamFetcher serves a streamed response for imgproxy tests. Body is the raw
// bytes streamed; Closes (when set) is incremented each time the returned Body is
// closed, so a test can assert exactly-once close / token release.
type StubStreamFetcher struct {
	Status        int
	ContentType   string
	ContentLength int64
	Body          string
	Err           error
	Closes        *int
}

func (f StubStreamFetcher) FetchStream(context.Context, core.FetchRequest) (*core.FetchStreamResponse, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return &core.FetchStreamResponse{
		Status:        f.Status,
		ContentType:   f.ContentType,
		ContentLength: f.ContentLength,
		Body:          &countingCloser{r: strings.NewReader(f.Body), closes: f.Closes},
	}, nil
}

type countingCloser struct {
	r      io.Reader
	closes *int
}

func (c *countingCloser) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *countingCloser) Close() error {
	if c.closes != nil {
		(*c.closes)++
	}
	return nil
}

type StubParser struct{ PF *core.ParsedFeed }

func (p StubParser) Parse([]byte, string, string) (*core.ParsedFeed, error) { return p.PF, nil }
func (p StubParser) Discover([]byte, string) ([]string, error)              { return nil, nil }

type PassSanitizer struct{}

func (PassSanitizer) Sanitize(h, _ string) string { return h }

// StubClock is a fixed clock; set T to control "now".
type StubClock struct{ T time.Time }

func (c StubClock) Now() time.Time { return c.T }

func DiscardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// StubExtractor returns HTML or Err from Extract, ignoring inputs.
type StubExtractor struct {
	HTML string
	Err  error
}

func (e StubExtractor) Extract(_ context.Context, _ string, _ []byte) (string, error) {
	return e.HTML, e.Err
}

// BlockingFetcher signals on started (once), blocks until release is closed,
// then errors. The once guard keeps it safe for multi-fetch call paths (e.g.
// HTML discovery, which fetches twice) — a bare close would panic on the second
// Fetch.
func BlockingFetcher(started chan<- struct{}, release <-chan struct{}) core.Fetcher {
	return blockingFetcher{started: started, release: release, once: &sync.Once{}}
}

type blockingFetcher struct {
	started chan<- struct{}
	release <-chan struct{}
	once    *sync.Once
}

func (f blockingFetcher) Fetch(ctx context.Context, _ core.FetchRequest) (*core.FetchResponse, error) {
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
	case <-ctx.Done():
	}
	return nil, errors.New("released")
}

// RecordingMetrics is a mutex-guarded core.Metrics fake that records every
// call so tests can assert on emitted results/durations/errors/ticks without
// standing up a real metrics backend. Use the Snapshot*/Get* methods rather
// than touching fields directly — recording happens from background
// goroutines (poller/scraper workers) concurrently with test assertions.
type RecordingMetrics struct {
	mu sync.Mutex

	pollResults     []core.PollResult
	scrapeResults   []core.ScrapeResult
	errors          []RecordedError
	pollDurations   []time.Duration
	scrapeDurations []time.Duration
	pollInflight    int
	scrapeInflight  int
	pollerTicks     []time.Time
	scraperTicks    []time.Time
}

// RecordedError pairs the component and reason passed to ErrorObserved.
type RecordedError struct {
	C core.ErrorComponent
	R core.ErrorReason
}

func (m *RecordingMetrics) FeedPollDone(result core.PollResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollResults = append(m.pollResults, result)
}

func (m *RecordingMetrics) ObserveFeedPoll(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollDurations = append(m.pollDurations, d)
}

func (m *RecordingMetrics) ScrapeDone(result core.ScrapeResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrapeResults = append(m.scrapeResults, result)
}

func (m *RecordingMetrics) ObserveArticleScrape(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrapeDurations = append(m.scrapeDurations, d)
}

func (m *RecordingMetrics) ErrorObserved(c core.ErrorComponent, r core.ErrorReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, RecordedError{C: c, R: r})
}

func (m *RecordingMetrics) AddPollInflight(delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollInflight += delta
}

func (m *RecordingMetrics) AddScrapeInflight(delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrapeInflight += delta
}

func (m *RecordingMetrics) PollerTicked(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollerTicks = append(m.pollerTicks, t)
}

func (m *RecordingMetrics) ScraperTicked(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scraperTicks = append(m.scraperTicks, t)
}

// SnapshotPollResults returns a copy of every result recorded via FeedPollDone, in order.
func (m *RecordingMetrics) SnapshotPollResults() []core.PollResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]core.PollResult(nil), m.pollResults...)
}

// SnapshotScrapeResults returns a copy of every result recorded via ScrapeDone, in order.
func (m *RecordingMetrics) SnapshotScrapeResults() []core.ScrapeResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]core.ScrapeResult(nil), m.scrapeResults...)
}

// SnapshotErrors returns a copy of every (component, reason) recorded via ErrorObserved, in order.
func (m *RecordingMetrics) SnapshotErrors() []RecordedError {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RecordedError(nil), m.errors...)
}

// SnapshotPollDurations returns a copy of every duration recorded via ObserveFeedPoll, in order.
func (m *RecordingMetrics) SnapshotPollDurations() []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Duration(nil), m.pollDurations...)
}

// SnapshotScrapeDurations returns a copy of every duration recorded via ObserveArticleScrape, in order.
func (m *RecordingMetrics) SnapshotScrapeDurations() []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Duration(nil), m.scrapeDurations...)
}

// PollInflight returns the current inflight-poll count (sum of all AddPollInflight deltas).
func (m *RecordingMetrics) PollInflight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pollInflight
}

// ScrapeInflight returns the current inflight-scrape count (sum of all AddScrapeInflight deltas).
func (m *RecordingMetrics) ScrapeInflight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scrapeInflight
}

// SnapshotPollerTicks returns a copy of every timestamp recorded via PollerTicked, in order.
func (m *RecordingMetrics) SnapshotPollerTicks() []time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Time(nil), m.pollerTicks...)
}

// SnapshotScraperTicks returns a copy of every timestamp recorded via ScraperTicked, in order.
func (m *RecordingMetrics) SnapshotScraperTicks() []time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Time(nil), m.scraperTicks...)
}

var (
	_ core.Fetcher    = StubFetcher{}
	_ core.FeedParser = StubParser{}
	_ core.Sanitizer  = PassSanitizer{}
	_ core.Clock      = StubClock{}
	_ core.Extractor  = StubExtractor{}
	_ core.Fetcher    = blockingFetcher{}
	_ core.Metrics    = (*RecordingMetrics)(nil)
)
