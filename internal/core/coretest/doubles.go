package coretest

import (
	"context"
	"io"
	"log/slog"
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

// FuncFetcher is a programmable Fetcher whose Fetch method returns a fixed
// response and error. Use it when you need per-call control that StubFetcher
// (which also holds a fixed pair) cannot express through composition alone.
type FuncFetcher struct {
	Resp *core.FetchResponse
	Err  error
}

func (f FuncFetcher) Fetch(context.Context, core.FetchRequest) (*core.FetchResponse, error) {
	return f.Resp, f.Err
}

// StubExtractor returns HTML or Err from Extract, ignoring inputs.
type StubExtractor struct {
	HTML string
	Err  error
}

func (e StubExtractor) Extract(_ context.Context, _ string, _ []byte) (string, error) {
	return e.HTML, e.Err
}

var (
	_ core.Fetcher    = StubFetcher{}
	_ core.Fetcher    = FuncFetcher{}
	_ core.FeedParser = StubParser{}
	_ core.Sanitizer  = PassSanitizer{}
	_ core.Clock      = StubClock{}
	_ core.Extractor  = StubExtractor{}
)
