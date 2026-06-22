package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/clock"
	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/fetch"
	"github.com/bcrisp4/bfeed/internal/observability"
	"github.com/bcrisp4/bfeed/internal/parse"
	"github.com/bcrisp4/bfeed/internal/sanitize"
	"github.com/bcrisp4/bfeed/internal/store/sqlite"
	"github.com/bcrisp4/bfeed/internal/web"
)

// End-to-end: subscribe to a local feed server, then assert the entry renders.
func TestEndToEndSubscribeAndRead(t *testing.T) {
	feed := `<?xml version="1.0"?><rss version="2.0"><channel><title>Local</title>
<link>https://local.test/</link><item><title>E2E Post</title><link>https://local.test/1</link>
<guid>e2e-1</guid><description>&lt;p&gt;body&lt;/p&gt;</description></item></channel></rss>`
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(feed))
	}))
	defer origin.Close()

	log := observability.NewLogger("error", "text")
	db, err := sqlite.Open(context.Background(), t.TempDir()+"/e2e.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlite.New(db)
	fetcher := fetch.New(fetch.Config{UserAgent: "bfeed-e2e", HostConcurrency: 2, Timeout: 5 * time.Second, MaxBytes: 1 << 20})
	feedSvc := core.NewFeedService(store, fetcher, parse.New(), sanitize.New(), clock.Real{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	entrySvc := core.NewEntryService(store, log)
	catSvc := core.NewCategoryService(store, log)
	searchSvc := core.NewSearchService(store, log)

	if _, err := feedSvc.Subscribe(context.Background(), core.DefaultUserID, origin.URL, nil, false); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	h := web.New(feedSvc, entrySvc, catSvc, searchSvc, log, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "E2E Post") {
		t.Fatalf("entry not shown after subscribe:\n%s", rec.Body.String())
	}
}
