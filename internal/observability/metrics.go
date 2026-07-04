package observability

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bcrisp4/bfeed/internal/core"
)

// durationBuckets are the histogram buckets shared by the feed-poll and
// article-scrape duration histograms.
var durationBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60}

// StatsCounter is the store-side dependency for the entity/backlog gauges
// (bfeed_users/bfeed_feeds/bfeed_entries/bfeed_poll_backlog/bfeed_scrape_backlog).
// Implemented by *sqlite.Store; kept here (not imported from store/sqlite) so
// this package never imports an adapter package.
type StatsCounter interface {
	CountUsers(ctx context.Context) (int64, error)
	CountFeeds(ctx context.Context) (int64, error)
	CountEntries(ctx context.Context) (int64, error)
	CountDueFeeds(ctx context.Context, now time.Time) (int64, error)
	CountDueExtractions(ctx context.Context, now time.Time) (int64, error)
}

// Metrics is the Prometheus adapter. It implements core.Metrics (the port
// core services emit into) plus HTTPRequest, which satisfies a
// consumer-owned interface in internal/web (not imported here — no
// adapter-to-adapter edge). This is the only package, besides cmd/bfeed,
// permitted to import prometheus.
type Metrics struct {
	reg *prometheus.Registry

	feedPollDuration    prometheus.Histogram
	scrapeDuration      prometheus.Histogram
	httpDuration        *prometheus.HistogramVec
	httpRequestsTotal   *prometheus.CounterVec
	feedPollsTotal      *prometheus.CounterVec
	articleScrapesTotal *prometheus.CounterVec
	errorsTotal         *prometheus.CounterVec
	pollInflight        prometheus.Gauge
	scrapeInflight      prometheus.Gauge
	pollerLastTick      prometheus.Gauge
	scraperLastTick     prometheus.Gauge
	buildInfo           *prometheus.GaugeVec
}

// allPollResults / allScrapeResults are the closed enums pre-registered at
// construction so the *_total counter vecs never appear to "jump" from
// absent to non-zero on first use -- rate() and increase() need the zero
// samples present from t=0.
var allPollResults = []core.PollResult{
	core.PollSuccess,
	core.PollNotModified,
	core.PollFetchError,
	core.PollHTTPError,
	core.PollParseError,
	core.PollStoreError,
	core.PollPanic,
}

var allScrapeResults = []core.ScrapeResult{
	core.ScrapeSuccess,
	core.ScrapeFetchError,
	core.ScrapeHTTPError,
	core.ScrapeExtractError,
	core.ScrapeRetried,
	core.ScrapeFailed,
}

// NewMetrics builds the registry and every instrument, registers the Go and
// process collectors, and sets bfeed_build_info{version} to 1.
func NewMetrics(version string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &Metrics{
		reg: reg,
		feedPollDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "bfeed_feed_poll_duration_seconds",
			Help:    "Duration of feed poll attempts, in seconds.",
			Buckets: durationBuckets,
		}),
		scrapeDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "bfeed_article_scrape_duration_seconds",
			Help:    "Duration of article scrape attempts, in seconds.",
			Buckets: durationBuckets,
		}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "bfeed_http_request_duration_seconds",
			Help:    "Duration of HTTP requests, in seconds, by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		httpRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bfeed_http_requests_total",
			Help: "Total HTTP requests handled, by route, method and status.",
		}, []string{"route", "method", "status"}),
		feedPollsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bfeed_feed_polls_total",
			Help: "Total feed poll attempts, by result.",
		}, []string{"result"}),
		articleScrapesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bfeed_article_scrapes_total",
			Help: "Total article scrape attempts, by result.",
		}, []string{"result"}),
		errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bfeed_errors_total",
			Help: "Total errors observed, by component and reason.",
		}, []string{"component", "reason"}),
		pollInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bfeed_poll_inflight",
			Help: "Number of feed polls currently in flight.",
		}),
		scrapeInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bfeed_scrape_inflight",
			Help: "Number of article scrapes currently in flight.",
		}),
		pollerLastTick: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bfeed_poller_last_tick_timestamp_seconds",
			Help: "Unix timestamp of the poller's last tick.",
		}),
		scraperLastTick: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "bfeed_scraper_last_tick_timestamp_seconds",
			Help: "Unix timestamp of the scraper's last tick.",
		}),
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bfeed_build_info",
			Help: "Build information; the metric value is always 1.",
		}, []string{"version"}),
	}

	// Pre-initialize every enum value of the two result vecs (not the
	// errors matrix) so rate()/increase() see the zero samples from the start.
	for _, r := range allPollResults {
		m.feedPollsTotal.WithLabelValues(string(r))
	}
	for _, r := range allScrapeResults {
		m.articleScrapesTotal.WithLabelValues(string(r))
	}
	m.buildInfo.WithLabelValues(version).Set(1)

	reg.MustRegister(
		m.feedPollDuration,
		m.scrapeDuration,
		m.httpDuration,
		m.httpRequestsTotal,
		m.feedPollsTotal,
		m.articleScrapesTotal,
		m.errorsTotal,
		m.pollInflight,
		m.scrapeInflight,
		m.pollerLastTick,
		m.scraperLastTick,
		m.buildInfo,
	)

	return m
}

// Gatherer exposes the underlying registry for exposition/testing without
// handing out Register access.
func (m *Metrics) Gatherer() prometheus.Gatherer {
	return m.reg
}

// FeedPollDone implements core.Metrics.
func (m *Metrics) FeedPollDone(result core.PollResult) {
	m.feedPollsTotal.WithLabelValues(string(result)).Inc()
}

// ObserveFeedPoll implements core.Metrics.
func (m *Metrics) ObserveFeedPoll(d time.Duration) {
	m.feedPollDuration.Observe(d.Seconds())
}

// ScrapeDone implements core.Metrics.
func (m *Metrics) ScrapeDone(result core.ScrapeResult) {
	m.articleScrapesTotal.WithLabelValues(string(result)).Inc()
}

// ObserveArticleScrape implements core.Metrics.
func (m *Metrics) ObserveArticleScrape(d time.Duration) {
	m.scrapeDuration.Observe(d.Seconds())
}

// ErrorObserved implements core.Metrics.
func (m *Metrics) ErrorObserved(c core.ErrorComponent, r core.ErrorReason) {
	m.errorsTotal.WithLabelValues(string(c), string(r)).Inc()
}

// AddPollInflight implements core.Metrics.
func (m *Metrics) AddPollInflight(delta int) {
	m.pollInflight.Add(float64(delta))
}

// AddScrapeInflight implements core.Metrics.
func (m *Metrics) AddScrapeInflight(delta int) {
	m.scrapeInflight.Add(float64(delta))
}

// PollerTicked implements core.Metrics.
func (m *Metrics) PollerTicked(t time.Time) {
	m.pollerLastTick.Set(float64(t.Unix()))
}

// ScraperTicked implements core.Metrics.
func (m *Metrics) ScraperTicked(t time.Time) {
	m.scraperLastTick.Set(float64(t.Unix()))
}

var _ core.Metrics = (*Metrics)(nil)

// HTTPRequest satisfies the consumer-owned HTTP metrics interface defined in
// internal/web (Task 8). Deliberately not asserted against a web type here --
// this package must not import internal/web (no adapter-to-adapter edge).
func (m *Metrics) HTTPRequest(route, method string, status int, d time.Duration) {
	m.httpDuration.WithLabelValues(route).Observe(d.Seconds())
	m.httpRequestsTotal.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
}

// RegisterDB registers a collectors.NewDBStatsCollector for db, exposing
// go_sql_* metrics labeled db_name="bfeed".
func (m *Metrics) RegisterDB(db *sql.DB) {
	m.reg.MustRegister(collectors.NewDBStatsCollector(db, "bfeed"))
}

// RegisterStats registers a statsCollector backed by c, exposing the
// bfeed_users/bfeed_feeds/bfeed_entries/bfeed_poll_backlog/bfeed_scrape_backlog
// gauges.
func (m *Metrics) RegisterStats(c StatsCounter) {
	m.reg.MustRegister(newStatsCollector(c))
}

// Handler returns an http.Handler serving GET /metrics (Prometheus exposition
// format) and GET /healthz ("ok").
func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// statsCollector is a custom prometheus.Collector wrapping a StatsCounter.
// Collect runs each count independently: an error on one count skips just
// that metric (no partial/garbage value) while the others are still reported.
type statsCollector struct {
	c StatsCounter

	users         *prometheus.Desc
	feeds         *prometheus.Desc
	entries       *prometheus.Desc
	pollBacklog   *prometheus.Desc
	scrapeBacklog *prometheus.Desc
}

func newStatsCollector(c StatsCounter) *statsCollector {
	return &statsCollector{
		c:             c,
		users:         prometheus.NewDesc("bfeed_users", "Total number of users.", nil, nil),
		feeds:         prometheus.NewDesc("bfeed_feeds", "Total number of feeds across all users.", nil, nil),
		entries:       prometheus.NewDesc("bfeed_entries", "Total number of entries across all users.", nil, nil),
		pollBacklog:   prometheus.NewDesc("bfeed_poll_backlog", "Number of feeds currently due to be polled.", nil, nil),
		scrapeBacklog: prometheus.NewDesc("bfeed_scrape_backlog", "Number of entries currently due to be scraped.", nil, nil),
	}
}

func (s *statsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.users
	ch <- s.feeds
	ch <- s.entries
	ch <- s.pollBacklog
	ch <- s.scrapeBacklog
}

func (s *statsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := time.Now()

	if n, err := s.c.CountUsers(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(s.users, prometheus.GaugeValue, float64(n))
	}
	if n, err := s.c.CountFeeds(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(s.feeds, prometheus.GaugeValue, float64(n))
	}
	if n, err := s.c.CountEntries(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(s.entries, prometheus.GaugeValue, float64(n))
	}
	if n, err := s.c.CountDueFeeds(ctx, now); err == nil {
		ch <- prometheus.MustNewConstMetric(s.pollBacklog, prometheus.GaugeValue, float64(n))
	}
	if n, err := s.c.CountDueExtractions(ctx, now); err == nil {
		ch <- prometheus.MustNewConstMetric(s.scrapeBacklog, prometheus.GaugeValue, float64(n))
	}
}

var _ prometheus.Collector = (*statsCollector)(nil)
