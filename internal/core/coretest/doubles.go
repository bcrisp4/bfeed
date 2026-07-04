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
		*c.closes++
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

var (
	_ core.Fetcher    = StubFetcher{}
	_ core.FeedParser = StubParser{}
	_ core.Sanitizer  = PassSanitizer{}
	_ core.Clock      = StubClock{}
	_ core.Extractor  = StubExtractor{}
	_ core.Fetcher    = blockingFetcher{}
)
