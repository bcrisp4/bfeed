package coretest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// SeedEntry inserts e into store via UpsertEntries and returns its assigned ID.
func SeedEntry(store *MemStore, e *core.Entry) core.ID {
	ins, err := store.UpsertEntries(context.Background(), e.FeedID, []*core.Entry{e})
	if err != nil {
		panic("SeedEntry: " + err.Error())
	}
	if len(ins) != 1 {
		panic("SeedEntry: entry not inserted (tombstone collision or duplicate GUID?)")
	}
	return ins[0].ID
}

type StubFetcher struct {
	Resp *core.FetchResponse
	Err  error
}

func (f StubFetcher) Fetch(context.Context, core.FetchRequest) (*core.FetchResponse, error) {
	return f.Resp, f.Err
}

type StubParser struct{ PF *core.ParsedFeed }

func (p StubParser) Parse([]byte, string) (*core.ParsedFeed, error) { return p.PF, nil }
func (p StubParser) Discover([]byte, string) ([]string, error)      { return nil, nil }

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
