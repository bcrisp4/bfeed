package observability_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	_ "modernc.org/sqlite"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/observability"
)

// exercise calls every core.Metrics method (plus HTTPRequest) once, covering
// every closed enum value, so lint/name assertions see a fully "warmed up"
// registry.
func exercise(m *observability.Metrics) {
	// Range over core's own canonical enum lists (F9) rather than a
	// hand-duplicated literal slice, so a new PollResult/ScrapeResult value
	// can't silently be added to the type without this "warmed up" registry
	// (and NewMetrics's pre-registration, which sources from the same lists)
	// picking it up.
	for _, r := range core.AllPollResults {
		m.FeedPollDone(r)
	}
	m.ObserveFeedPoll(1200 * time.Millisecond)

	for _, r := range core.AllScrapeResults {
		m.ScrapeDone(r)
	}
	m.ObserveArticleScrape(800 * time.Millisecond)

	m.ErrorObserved(core.CompFeedPoll, core.ReasonTimeout)
	m.AddPollInflight(1)
	m.AddPollInflight(-1)
	m.AddScrapeInflight(2)
	m.AddScrapeInflight(-2)
	m.PollerTicked(time.Now())
	m.ScraperTicked(time.Now())
	m.HTTPRequest("/entries", "GET", 200, 15*time.Millisecond)
}

func TestMetricsLint(t *testing.T) {
	m := observability.NewMetrics("test")
	exercise(m)

	problems, err := testutil.GatherAndLint(m.Gatherer())
	if err != nil {
		t.Fatalf("GatherAndLint: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("lint problems: %+v", problems)
	}
}

func TestMetricNames(t *testing.T) {
	m := observability.NewMetrics("v1.2.3")
	exercise(m)

	families, err := m.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	byName := make(map[string]int)
	for _, f := range families {
		byName[f.GetName()]++
	}

	want := []string{
		"bfeed_feed_poll_duration_seconds",
		"bfeed_article_scrape_duration_seconds",
		"bfeed_http_request_duration_seconds",
		"bfeed_http_requests_total",
		"bfeed_feed_polls_total",
		"bfeed_article_scrapes_total",
		"bfeed_errors_total",
		"bfeed_poll_inflight",
		"bfeed_scrape_inflight",
		"bfeed_poller_last_tick_timestamp_seconds",
		"bfeed_scraper_last_tick_timestamp_seconds",
		"bfeed_build_info",
	}
	for _, name := range want {
		if byName[name] == 0 {
			t.Errorf("missing metric family %q", name)
		}
	}

	// bfeed_build_info carries the version label and is set to 1.
	found := false
	for _, f := range families {
		if f.GetName() != "bfeed_build_info" {
			continue
		}
		for _, metric := range f.GetMetric() {
			for _, lbl := range metric.GetLabel() {
				if lbl.GetName() == "version" && lbl.GetValue() == "v1.2.3" {
					found = true
					if metric.GetGauge().GetValue() != 1 {
						t.Errorf("bfeed_build_info value = %v, want 1", metric.GetGauge().GetValue())
					}
				}
			}
		}
	}
	if !found {
		t.Errorf("bfeed_build_info missing version=%q label", "v1.2.3")
	}
}

type fakeStats struct {
	users, feeds, entries, dueFeeds, dueExtractions int64
	usersErr                                        error
}

func (f fakeStats) CountUsers(context.Context) (int64, error)   { return f.users, f.usersErr }
func (f fakeStats) CountFeeds(context.Context) (int64, error)   { return f.feeds, nil }
func (f fakeStats) CountEntries(context.Context) (int64, error) { return f.entries, nil }

func (f fakeStats) CountDueFeeds(context.Context, time.Time) (int64, error) {
	return f.dueFeeds, nil
}

func (f fakeStats) CountDueExtractions(context.Context, time.Time) (int64, error) {
	return f.dueExtractions, nil
}

func TestStatsCollector(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := observability.NewMetrics("test")
		m.RegisterStats(fakeStats{users: 3, feeds: 5, entries: 100, dueFeeds: 2, dueExtractions: 1})

		families, err := m.Gatherer().Gather()
		if err != nil {
			t.Fatalf("gather: %v", err)
		}
		byName := make(map[string]int)
		for _, f := range families {
			byName[f.GetName()]++
		}
		for _, name := range []string{"bfeed_users", "bfeed_feeds", "bfeed_entries", "bfeed_poll_backlog", "bfeed_scrape_backlog"} {
			if byName[name] == 0 {
				t.Errorf("missing metric family %q", name)
			}
		}
	})

	t.Run("one method errors", func(t *testing.T) {
		m := observability.NewMetrics("test")
		m.RegisterStats(fakeStats{
			feeds: 5, entries: 100, dueFeeds: 2, dueExtractions: 1,
			usersErr: errors.New("boom"),
		})

		families, err := m.Gatherer().Gather()
		if err != nil {
			t.Fatalf("gather: %v", err)
		}
		byName := make(map[string]int)
		for _, f := range families {
			byName[f.GetName()]++
		}
		if byName["bfeed_users"] != 0 {
			t.Errorf("bfeed_users should be absent when CountUsers errors")
		}
		for _, name := range []string{"bfeed_feeds", "bfeed_entries", "bfeed_poll_backlog", "bfeed_scrape_backlog"} {
			if byName[name] == 0 {
				t.Errorf("missing metric family %q", name)
			}
		}
	})
}

func TestHandler(t *testing.T) {
	m := observability.NewMetrics("test")

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bfeed_feed_polls_total") {
		t.Errorf("/metrics body missing bfeed_feed_polls_total")
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m.RegisterDB(db)

	rec2 := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("/metrics (after RegisterDB) status = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "go_sql_") {
		t.Errorf("/metrics body missing go_sql_ after RegisterDB")
	}

	rec3 := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec3.Code)
	}
	if rec3.Body.String() != "ok" {
		t.Errorf("/healthz body = %q, want %q", rec3.Body.String(), "ok")
	}
}
