package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/web"
)

// httpCall records one invocation of fakeMetrics.HTTPRequest for assertion.
type httpCall struct {
	route  string
	method string
	status int
}

// fakeMetrics is a minimal, package-local double satisfying web.HTTPMetrics —
// it only needs to exist here (used by this file's tests only), so it isn't
// promoted to coretest.
type fakeMetrics struct {
	mu    sync.Mutex
	calls []httpCall
}

func (f *fakeMetrics) HTTPRequest(route, method string, status int, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, httpCall{route: route, method: method, status: status})
}

func (f *fakeMetrics) recorded() []httpCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]httpCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// newMetricsHandler builds a web.Handler wired with the given metrics
// recorder and readiness probe, so instrument()/​readyz behaviour can be
// exercised independently of the rest of the web test suite's fixed helpers.
func newMetricsHandler(t *testing.T, expectedHost string, metrics web.HTTPMetrics, ready func(context.Context) error) (http.Handler, *coretest.MemStore) {
	t.Helper()
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	return web.New(fs, es, cs, ss, log, nil, nil, 20, expectedHost, metrics, ready), store
}

func TestReadyzOKWhenReadyFuncNil(t *testing.T) {
	h, _ := newMetricsHandler(t, "", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status=%d body=%q, want 200 \"ok\"", rec.Code, rec.Body.String())
	}
}

func TestReadyzReturns503WhenReadyFuncErrors(t *testing.T) {
	h, _ := newMetricsHandler(t, "", nil, func(context.Context) error { return errors.New("down") })
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
}

func TestReadyzOKWhenReadyFuncSucceeds(t *testing.T) {
	h, _ := newMetricsHandler(t, "", nil, func(context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
}

// /readyz must be reachable even with a mismatched Host header — same
// exemption rationale as /healthz (e.g. a container orchestrator's readiness
// probe hitting a loopback address).
func TestReadyzExemptFromHostGuard(t *testing.T) {
	h, _ := newMetricsHandler(t, "bfeed.example", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	req.Host = "127.0.0.1:9999"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 despite mismatched Host", rec.Code)
	}
}

// A matched route records the ServeMux pattern, the request method, and the
// response status the handler actually produced.
func TestInstrumentRecordsRouteMethodStatus(t *testing.T) {
	fm := &fakeMetrics{}
	h, _ := newMetricsHandler(t, "", fm, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	calls := fm.recorded()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1: %+v", len(calls), calls)
	}
	if calls[0].route != "GET /{$}" || calls[0].method != "GET" || calls[0].status != 200 {
		t.Fatalf("got %+v, want route=%q method=GET status=200", calls[0], "GET /{$}")
	}
}

// An unmatched path (404) has an empty r.Pattern — must fall back to the
// "unmatched" label rather than recording an empty route (unbounded
// cardinality if the raw path were used instead).
func TestInstrumentRecordsUnmatchedRouteOn404(t *testing.T) {
	fm := &fakeMetrics{}
	h, _ := newMetricsHandler(t, "", fm, nil)
	req := httptest.NewRequest(http.MethodGet, "/this-path-does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
	calls := fm.recorded()
	if len(calls) != 1 || calls[0].route != "unmatched" || calls[0].status != 404 {
		t.Fatalf("got %+v, want route=unmatched status=404", calls)
	}
}

// A method outside the allowlist must be recorded as "other" — an open-ended
// method label would blow metric cardinality.
func TestInstrumentRecordsOtherForUnlistedMethod(t *testing.T) {
	fm := &fakeMetrics{}
	h, _ := newMetricsHandler(t, "", fm, nil)
	req := httptest.NewRequest("PROPFIND", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	calls := fm.recorded()
	if len(calls) != 1 || calls[0].method != "other" {
		t.Fatalf("got %+v, want method=other", calls)
	}
}

// A nil metrics recorder must not panic and must not be called.
func TestInstrumentNilMetricsIsNoop(t *testing.T) {
	h, _ := newMetricsHandler(t, "", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // must not panic
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
}

// A handler that never calls WriteHeader (writes body directly) must be
// recorded as status 200, the http.ResponseWriter default.
func TestInstrumentDefaultsStatusTo200WhenWriteHeaderNotCalled(t *testing.T) {
	fm := &fakeMetrics{}
	h, _ := newMetricsHandler(t, "", fm, nil)
	// /static/app.css is served by http.FileServer, which for a successful
	// GET writes the body without an explicit WriteHeader call.
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	calls := fm.recorded()
	if len(calls) != 1 || calls[0].status != 200 {
		t.Fatalf("got %+v, want status=200", calls)
	}
}
