package coretest

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

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

var (
	_ core.Fetcher    = StubFetcher{}
	_ core.FeedParser = StubParser{}
	_ core.Sanitizer  = PassSanitizer{}
	_ core.Clock      = StubClock{}
)
