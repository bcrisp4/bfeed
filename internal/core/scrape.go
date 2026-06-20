package core

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// ScrapeConfig holds tunable knobs for the per-entry extraction pipeline.
type ScrapeConfig struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// scrapeStore is the narrow persistence surface ScrapeService needs.
type scrapeStore interface {
	// SetEntryContent stores the sanitised HTML and transitions extract_state to 'done'.
	SetEntryContent(ctx context.Context, entryID ID, content string) error
	UpdateExtractState(ctx context.Context, entryID ID, state ExtractState, attempts int, nextAt *time.Time) error
}

// ScrapeService fetches, extracts, sanitises, and persists full article content
// for a single entry. It honours the sanitise-before-persist invariant and
// schedules retries with exponential backoff when extraction fails.
type ScrapeService struct {
	store   scrapeStore
	fetcher Fetcher
	ext     Extractor
	san     Sanitizer
	clk     Clock
	log     *slog.Logger
	cfg     ScrapeConfig
	jitter  func(time.Duration) time.Duration
}

// NewScrapeService constructs a ScrapeService with sane defaults for any
// zero-value config fields.
func NewScrapeService(
	store scrapeStore,
	fetcher Fetcher,
	ext Extractor,
	san Sanitizer,
	clk Clock,
	log *slog.Logger,
	cfg ScrapeConfig,
	jitter func(time.Duration) time.Duration,
) *ScrapeService {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 10 * time.Minute
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 24 * time.Hour
	}
	return &ScrapeService{
		store:   store,
		fetcher: fetcher,
		ext:     ext,
		san:     san,
		clk:     clk,
		log:     log,
		cfg:     cfg,
		jitter:  jitter,
	}
}

// Compile-time assertion that ScrapeService satisfies EntryScraper.
var _ EntryScraper = (*ScrapeService)(nil)

// ScrapeEntry fetches the entry URL, extracts main content, sanitises it, and
// replaces the stored content. On any failure it records a retry (with backoff)
// or, past the attempt cap, marks extraction terminally failed — keeping the
// feed-provided content either way.
func (s *ScrapeService) ScrapeEntry(ctx context.Context, e *Entry) error {
	resp, err := s.fetcher.Fetch(ctx, FetchRequest{URL: e.URL})
	if err != nil {
		return s.fail(ctx, e, "fetch: "+err.Error())
	}
	if resp.Status != 200 || !isHTML(resp.ContentType) {
		return s.fail(ctx, e, "non-html or non-200 status")
	}
	html, err := s.ext.Extract(ctx, e.URL, resp.Body)
	if err != nil || strings.TrimSpace(html) == "" {
		return s.fail(ctx, e, "extract")
	}
	safe := s.san.Sanitize(html, e.URL) // sanitise-before-persist invariant
	return s.store.SetEntryContent(ctx, e.ID, safe)
}

// fail records a failed extraction attempt. If the attempt cap is reached it
// marks the entry as terminally failed; otherwise it schedules a retry with
// exponential backoff.
func (s *ScrapeService) fail(ctx context.Context, e *Entry, reason string) error {
	attempts := e.ExtractAttempts + 1
	if attempts >= s.cfg.MaxAttempts {
		s.log.Warn("extraction failed (terminal)", "entry_id", int64(e.ID), "url", e.URL, "reason", reason)
		return s.store.UpdateExtractState(ctx, e.ID, ExtractFailed, attempts, nil)
	}
	next := s.clk.Now().Add(ExtractBackoff(s.cfg, attempts, s.jitter))
	s.log.Info("extraction retry scheduled", "entry_id", int64(e.ID), "attempt", attempts, "reason", reason)
	return s.store.UpdateExtractState(ctx, e.ID, ExtractPending, attempts, &next)
}

// ExtractBackoff returns BaseBackoff*2^(attempt-1), capped at MaxBackoff, plus
// optional jitter. attempt is 1-based (first failure = attempt 1).
func ExtractBackoff(cfg ScrapeConfig, attempt int, jitter func(time.Duration) time.Duration) time.Duration {
	d := cfg.BaseBackoff
	for i := 1; i < attempt && d < cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > cfg.MaxBackoff || d <= 0 {
		d = cfg.MaxBackoff
	}
	if jitter != nil {
		d += jitter(d)
	}
	return d
}

// isHTML reports whether the Content-Type header indicates an HTML document.
func isHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}
