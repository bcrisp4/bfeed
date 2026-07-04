package core

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ScraperConfig controls the Scraper's timing and concurrency.
type ScraperConfig struct {
	Tick    time.Duration
	Batch   int
	Workers int
}

// pendingLister is the narrow store surface the Scraper dispatches from.
type pendingLister interface {
	ListPendingExtractions(ctx context.Context, now time.Time, limit int) ([]*Entry, error)
}

// Scraper is the extraction analogue of Poller: it sweeps pending extractions
// each tick into a bounded worker pool calling EntryScraper.ScrapeEntry.
type Scraper struct {
	store   pendingLister
	scr     EntryScraper
	clk     Clock
	log     *slog.Logger
	cfg     ScraperConfig
	metrics Metrics
}

// NewScraper returns a Scraper ready to Run. Zero-value cfg fields are
// replaced with sensible defaults (Workers=20, Batch=50, Tick=1m).
func NewScraper(store pendingLister, scr EntryScraper, clk Clock, log *slog.Logger, cfg ScraperConfig) *Scraper {
	if cfg.Workers <= 0 {
		cfg.Workers = 20
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 50
	}
	if cfg.Tick <= 0 {
		cfg.Tick = time.Minute
	}
	return &Scraper{store: store, scr: scr, clk: clk, log: log, cfg: cfg, metrics: NopMetrics{}}
}

// SetMetrics wires the observability port after construction (nil is ignored,
// leaving the NopMetrics default from NewScraper — the Scraper never
// nil-checks its injected Metrics port).
func (p *Scraper) SetMetrics(m Metrics) {
	if m == nil {
		return
	}
	p.metrics = m
}

// Run blocks until ctx is cancelled, draining pending extractions each tick.
func (p *Scraper) Run(ctx context.Context) {
	jobs := make(chan *Entry)
	var wg sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				p.scrapeOne(ctx, e)
			}
		}()
	}
	ticker := time.NewTicker(p.cfg.Tick)
	defer ticker.Stop()
	p.dispatch(ctx, jobs)
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
			p.dispatch(ctx, jobs)
		}
	}
}

// scrapeOne runs one extraction, guarded so a panic on untrusted article HTML
// degrades to a logged error rather than killing the worker goroutine (and the
// process).
func (p *Scraper) scrapeOne(ctx context.Context, e *Entry) {
	defer RecoverGuard(p.log, "scrape worker", "entry_id", int64(e.ID))
	p.metrics.AddScrapeInflight(1)
	defer p.metrics.AddScrapeInflight(-1)
	if err := p.scr.ScrapeEntry(ctx, e); err != nil {
		p.log.Error("scrape entry", "entry_id", int64(e.ID), "error", err)
	}
}

func (p *Scraper) dispatch(ctx context.Context, jobs chan<- *Entry) {
	now := p.clk.Now()
	p.metrics.ScraperTicked(now)
	due, err := p.store.ListPendingExtractions(ctx, now, p.cfg.Batch)
	if err != nil {
		p.log.Error("list pending extractions", "error", err)
		if !errors.Is(err, context.Canceled) {
			// F3: a cancelled dispatch (shutdown) isn't a scrape/dispatch
			// failure worth counting. F8: a dispatch-level list failure is a
			// store-layer (db) problem, not an article_scrape attempt —
			// attributing it to CompArticleScrape would skew the
			// article_scrape error ratio, a metric meant to track scrape
			// *attempts*, not dispatch plumbing.
			p.metrics.ErrorObserved(CompDB, ReasonInternal)
		}
		return
	}
	for _, e := range due {
		select {
		case <-ctx.Done():
			return
		case jobs <- e:
		}
	}
}
