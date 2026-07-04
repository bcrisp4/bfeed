package core

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
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
	UpdateExtractState(ctx context.Context, entryID ID, state ExtractState, attempts int, nextAt *time.Time, reason string) error
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
	metrics Metrics
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
		metrics: NopMetrics{},
	}
}

// Compile-time assertion that ScrapeService satisfies EntryScraper.
var _ EntryScraper = (*ScrapeService)(nil)

// SetMetrics wires the observability port after construction (nil is ignored,
// leaving the NopMetrics default from NewScrapeService — services never
// nil-check their injected Metrics port).
func (s *ScrapeService) SetMetrics(m Metrics) {
	if m == nil {
		return
	}
	s.metrics = m
}

// ScrapeEntry fetches the entry URL, extracts main content, sanitises it, and
// replaces the stored content. On any failure it records a retry (with backoff)
// or, past the attempt cap, marks extraction terminally failed — keeping the
// feed-provided content either way.
func (s *ScrapeService) ScrapeEntry(ctx context.Context, e *Entry) (err error) {
	start := s.clk.Now()
	defer func() {
		s.metrics.ObserveArticleScrape(s.clk.Now().Sub(start))
	}()
	defer func() {
		if r := recover(); r != nil {
			// A panic while extracting/sanitising untrusted article HTML degrades
			// to a recorded extraction failure (with backoff) instead of crashing
			// the scrape goroutine. See RecoverGuard.
			s.log.Error("recovered panic scraping entry",
				"entry_id", int64(e.ID), "url", e.URL, "panic", r, "stack", string(debug.Stack()))
			err = s.fail(ctx, e, fmt.Sprintf("panic: %v", r), ScrapeFailed, ReasonInternal)
		}
	}()
	resp, err := s.fetcher.Fetch(ctx, FetchRequest{URL: e.URL})
	if err != nil {
		if ctxShutdownCanceled(ctx, err) {
			// F3: the fetch failed because the scrape's own ctx (shutdown) was
			// cancelled — persist the retry/backoff state as usual, but don't
			// count it: a stopped worker isn't a scrape failure.
			return s.failEmit(ctx, e, "fetch: "+err.Error(), ScrapeFetchError, ClassifyFetchError(err), false)
		}
		return s.fail(ctx, e, "fetch: "+err.Error(), ScrapeFetchError, ClassifyFetchError(err))
	}
	if resp.Status != 200 {
		// F9: shared ladder with PollFeed's status handling (classifyHTTPStatus in
		// metrics.go) — 429/5xx retry without burning an attempt (mirrors
		// PollFeed's 429/5xx branch: a full-content backfill burst that trips a
		// rate limit must not convert a whole feed's backlog to terminal
		// extraction failures, audit B10); anything else non-200 burns one.
		reason := classifyHTTPStatus(resp.Status)
		msg := fmt.Sprintf("status %d", resp.Status)
		if reason == ReasonRateLimited || reason == ReasonHTTP5xx {
			return s.retryLater(ctx, e, msg, resp.RetryAfter, reason)
		}
		return s.fail(ctx, e, msg, ScrapeHTTPError, reason)
	}
	if !isHTML(resp.ContentType) {
		// F2: a 200 with a non-HTML body is a content-shape problem (e.g. the
		// entry URL serves a PDF or an image), not an HTTP-status failure — file
		// it under extract_error/parse, not the http_error/http_4xx bucket the
		// combined guard used to give it.
		return s.fail(ctx, e, fmt.Sprintf("content-type %q", resp.ContentType), ScrapeExtractError, ReasonParse)
	}
	// Resolve relative links against the post-redirect URL: fetching e.URL may
	// have followed redirects (feedproxy, tracking, a moved domain), and the page
	// body's relative img/href are relative to where it was actually served.
	pageURL := resp.FinalURL
	if pageURL == "" {
		pageURL = e.URL
	}
	html, err := s.ext.Extract(ctx, pageURL, resp.Body)
	if err != nil {
		return s.fail(ctx, e, "extract: "+err.Error(), ScrapeExtractError, ReasonParse)
	}
	if strings.TrimSpace(html) == "" {
		return s.fail(ctx, e, "extract: empty content", ScrapeExtractError, ReasonParse)
	}
	safe := s.san.Sanitize(html, pageURL) // sanitise-before-persist invariant
	if strings.TrimSpace(safe) == "" {
		// Extraction yielded only content the sanitiser stripped; treat as a
		// failure rather than overwriting the feed-provided content with nothing.
		return s.fail(ctx, e, "sanitised content empty", ScrapeExtractError, ReasonParse)
	}
	if err := s.store.SetEntryContent(ctx, e.ID, safe); err != nil {
		// A persist failure must reschedule with backoff (via fail), not return
		// raw: returning here leaves the entry pending with next_extract_at in the
		// past and attempts unincremented, so the Scraper retries it every tick.
		return s.fail(ctx, e, "persist: "+err.Error(), ScrapeFailed, ReasonInternal)
	}
	s.metrics.ScrapeDone(ScrapeSuccess)
	return nil
}

// fail records a failed extraction attempt and emits its terminal ScrapeDone
// (result) + ErrorObserved (reason) pair — exactly once, regardless of whether
// this particular attempt lands terminal or reschedules with backoff. If the
// attempt cap is reached it marks the entry as terminally failed; otherwise it
// schedules a retry with exponential backoff.
func (s *ScrapeService) fail(ctx context.Context, e *Entry, msg string, result ScrapeResult, reason ErrorReason) error {
	return s.failEmit(ctx, e, msg, result, reason, true)
}

// failEmit is fail's implementation, with an emit flag: F3's shutdown-cancelled
// fetch path still needs the retry/backoff state persisted (unchanged), but
// must not pollute bfeed_article_scrapes_total/bfeed_errors_total — a stopped
// worker isn't a scrape failure worth counting or alerting on.
func (s *ScrapeService) failEmit(ctx context.Context, e *Entry, msg string, result ScrapeResult, reason ErrorReason, emit bool) error {
	attempts := e.ExtractAttempts + 1
	var werr error
	if attempts >= s.cfg.MaxAttempts {
		s.log.Warn("extraction failed (terminal)", "entry_id", int64(e.ID), "url", e.URL, "reason", msg)
		werr = s.store.UpdateExtractState(ctx, e.ID, ExtractFailed, attempts, nil, msg)
	} else {
		next := s.clk.Now().Add(ExtractBackoff(s.cfg, attempts, s.jitter))
		s.log.Info("extraction retry scheduled", "entry_id", int64(e.ID), "attempt", attempts, "reason", msg)
		werr = s.store.UpdateExtractState(ctx, e.ID, ExtractPending, attempts, &next, msg)
	}
	// F10: emit after the store write (still emit even when it failed — the
	// attempt is over either way) so a panic mid-write can't cause a re-entrant
	// call to double-count this attempt's metrics.
	if emit {
		s.metrics.ScrapeDone(result)
		s.metrics.ErrorObserved(CompArticleScrape, reason)
	}
	return werr
}

// retryLater reschedules a transient failure (429/5xx) WITHOUT incrementing the
// attempt count, so a temporarily rate-limited or down host never exhausts the
// attempt cap. Backs off at least BaseBackoff, honouring a longer Retry-After, but
// caps the wait at MaxBackoff so a hostile/misconfigured Retry-After can't park an
// entry for years — matching the ceiling fail()/ExtractBackoff already enforces.
// Always emits ScrapeDone(ScrapeRetried) + ErrorObserved(reason) — exactly once
// per call, mirroring fail().
func (s *ScrapeService) retryLater(ctx context.Context, e *Entry, msg string, retryAfter time.Duration, reason ErrorReason) error {
	next := s.clk.Now().Add(min(max(s.cfg.BaseBackoff, retryAfter), s.cfg.MaxBackoff))
	s.log.Info("extraction deferred (transient)", "entry_id", int64(e.ID), "reason", msg, "retry_after", retryAfter)
	werr := s.store.UpdateExtractState(ctx, e.ID, ExtractPending, e.ExtractAttempts, &next, msg)
	// F10: emit after the store write, mirroring fail()/failEmit.
	s.metrics.ScrapeDone(ScrapeRetried)
	s.metrics.ErrorObserved(CompArticleScrape, reason)
	return werr
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
