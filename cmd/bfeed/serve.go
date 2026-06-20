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
	"github.com/bcrisp4/bfeed/internal/fetch"
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
		UserAgent:       fmt.Sprintf("bfeed/%s (+%s)", version, cfg.BaseURL),
		HostConcurrency: cfg.HostConcurrency, Timeout: 30 * time.Second, MaxBytes: 10 << 20,
	})
	jitter := func(d time.Duration) time.Duration {
		if d <= 0 {
			return 0
		}
		return time.Duration(rand.Int63n(int64(d) / 4)) //nolint:gosec // G404: jitter, not security-sensitive
	}
	feedSvc := core.NewFeedService(store, fetcher, parse.New(), sanitize.New(), clock.Real{}, log,
		core.FeedServiceConfig{
			Reschedule: core.RescheduleConfig{Interval: cfg.PollInterval, MaxBackoff: cfg.MaxBackoff},
			Jitter:     jitter,
		})
	entrySvc := core.NewEntryService(store, log)
	catSvc := core.NewCategoryService(store, log)
	searchSvc := core.NewSearchService(store, log)
	poller := core.NewPoller(store, feedSvc, clock.Real{}, log,
		core.PollerConfig{Tick: cfg.PollTick, BatchSize: cfg.BatchSize, Workers: cfg.FeedWorkers})

	pollerDone := make(chan struct{})
	go func() { poller.Run(ctx); close(pollerDone) }()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           web.New(feedSvc, entrySvc, catSvc, searchSvc, log),
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
	return 0
}
