package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bcrisp4/bfeed/internal/clock"
	"github.com/bcrisp4/bfeed/internal/config"
	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/extract"
	"github.com/bcrisp4/bfeed/internal/fetch"
	"github.com/bcrisp4/bfeed/internal/imgproxy"
	"github.com/bcrisp4/bfeed/internal/observability"
	"github.com/bcrisp4/bfeed/internal/parse"
	"github.com/bcrisp4/bfeed/internal/sanitize"
	"github.com/bcrisp4/bfeed/internal/store/sqlite"
	"github.com/bcrisp4/bfeed/internal/web"
)

func runServe() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log := observability.NewLogger(cfg.LogLevel, cfg.LogFormat)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		log.Error("open db", "error", err)
		return 1
	}
	defer db.Close()
	store := sqlite.New(db)

	fetcher := fetch.New(fetch.Config{
		UserAgent:            fmt.Sprintf("bfeed/%s (+%s)", version, cfg.BaseURL),
		HostConcurrency:      cfg.HostConcurrency,
		Timeout:              30 * time.Second,
		MaxBytes:             10 << 20,
		BlockPrivateNetworks: cfg.BlockPrivateNetworks,
		AllowedCIDRs:         cfg.AllowPrivateCIDRs,
	})
	jitter := func(d time.Duration) time.Duration {
		n := int64(d) / 4
		if n <= 0 { // d < 4ns: nothing to jitter, and rand.Int63n panics on n<=0
			return 0
		}
		return time.Duration(rand.Int63n(n)) //nolint:gosec // G404: jitter, not security-sensitive
	}
	san := sanitize.New()
	feedSvc := core.NewFeedService(store, fetcher, parse.New(), san, clock.Real{}, log,
		core.FeedServiceConfig{
			Schedule:   core.ScheduleConfig{MinInterval: cfg.SchedMinInterval, MaxInterval: cfg.SchedMaxInterval, Factor: cfg.SchedFactor},
			Reschedule: core.RescheduleConfig{Interval: cfg.SchedMinInterval, MaxBackoff: cfg.MaxBackoff},
			Jitter:     jitter,
		})
	entrySvc := core.NewEntryService(store, log)
	catSvc := core.NewCategoryService(store, log)
	searchSvc := core.NewSearchService(store, log)
	poller := core.NewPoller(store, feedSvc, clock.Real{}, log,
		core.PollerConfig{Tick: cfg.PollTick, BatchSize: cfg.BatchSize, Workers: cfg.FeedWorkers})

	// Scrape backoff base/cap are internal constants (NewScrapeService defaults),
	// kept independent of the polling knobs (BFEED_MAX_BACKOFF) per the spec.
	scrapeSvc := core.NewScrapeService(store, fetcher, extract.New(), san, clock.Real{}, log,
		core.ScrapeConfig{MaxAttempts: cfg.ScrapeMaxAttempts},
		jitter)
	scraper := core.NewScraper(store, scrapeSvc, clock.Real{}, log,
		core.ScraperConfig{Tick: cfg.ScrapeTick, Batch: cfg.ScrapeBatch, Workers: cfg.ScrapeWorkers})

	pollerDone := make(chan struct{})
	go func() { poller.Run(ctx); close(pollerDone) }()

	scraperDone := make(chan struct{})
	go func() { scraper.Run(ctx); close(scraperDone) }()

	var imgHandler http.Handler
	var imgRewrite func(string) string
	if cfg.ImageProxy {
		if s := cfg.ImageProxySecret; s != "" && len(s) < 16 {
			log.Warn("BFEED_IMAGE_PROXY_SECRET is short; prefer >= 32 random bytes for a strong signing key", "len", len(s))
		}
		secret, err := imgproxy.ResolveSecret(ctx, store, cfg.ImageProxySecret)
		if err != nil {
			log.Error("image proxy secret", "error", err)
			return 1
		}
		signer := imgproxy.NewSigner(secret)
		imgHandler = imgproxy.New(fetcher, signer, log)
		imgRewrite = signer.ProxyURL
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           web.New(feedSvc, entrySvc, catSvc, searchSvc, log, imgHandler, imgRewrite, cfg.FeedErrorLimit),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("shutdown", "error", err)
	}
	select {
	case <-pollerDone:
	case <-time.After(15 * time.Second):
		log.Warn("poller did not drain in time")
	}
	select {
	case <-scraperDone:
	case <-time.After(15 * time.Second):
		log.Warn("scraper did not drain in time")
	}
	return 0
}
