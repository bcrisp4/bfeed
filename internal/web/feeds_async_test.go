package web_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/web"
)

// newTestHandler builds a web Handler wired with coretest fakes using the
// provided fetcher. Returns the http.Handler and the backing MemStore.
func newTestHandler(t *testing.T, fetcher core.Fetcher) (http.Handler, *coretest.MemStore) {
	t.Helper()
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, fetcher, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	return web.New(fs, es, cs, ss, log, nil, nil, 20), store
}

// seedFeed inserts a feed that has been checked at least once (CheckedAt set),
// so that a refresh request sets Refreshing (not Pending).
func seedFeed(t *testing.T, store *coretest.MemStore) core.ID {
	t.Helper()
	ctx := t.Context()
	now := time.Unix(1_000_000, 0).UTC()
	id, err := store.CreateFeed(ctx, &core.Feed{
		UserID:      core.DefaultUserID,
		FeedURL:     "https://example.com/feed.xml",
		Title:       "Test Feed",
		CheckedAt:   &now,
		NextCheckAt: now.Add(-time.Minute), // overdue → eligible for refresh
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("seedFeed: %v", err)
	}
	return id
}

// itoa converts a core.ID to its decimal string representation.
func itoa(id core.ID) string {
	return strconv.FormatInt(int64(id), 10)
}

// do sends a request against the handler and returns the recorder.
func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRefreshIsNonBlockingAndReturnsPollingRow(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	fetcher := coretest.BlockingFetcher(started, release)
	h, st := newTestHandler(t, fetcher)
	id := seedFeed(t, st)

	req := httptest.NewRequest("POST", "/feeds/"+itoa(id)+"/refresh", nil)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(rec, req); close(done) }()

	select {
	case <-done: // handler returned without waiting on the fetch
	case <-time.After(2 * time.Second):
		t.Fatal("refresh handler blocked on background fetch")
	}
	<-started // background goroutine did reach the fetcher
	if !strings.Contains(rec.Body.String(), `hx-trigger="every 1500ms"`) {
		t.Errorf("refreshing row should poll, body=%s", rec.Body.String())
	}
	close(release)
}

func TestFeedRowFragmentStopsPollingWhenDone(t *testing.T) {
	h, st := newTestHandler(t, coretest.StubFetcher{})
	id := seedFeed(t, st)

	rec := do(t, h, "GET", "/feeds/"+itoa(id)+"/row")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /feeds/%d/row status %d", id, rec.Code)
	}
	body := rec.Body.String()
	// Feed is idle (not in-flight) so the row must NOT carry the poll trigger.
	if strings.Contains(body, `hx-trigger="every 1500ms"`) {
		t.Errorf("idle row should not carry poll trigger, body=%s", body)
	}
}

func TestFeedRowFragmentIncludesOOBGroupHeadWhenIdle(t *testing.T) {
	h, st := newTestHandler(t, coretest.StubFetcher{})
	id := seedFeed(t, st)

	rec := do(t, h, "GET", "/feeds/"+itoa(id)+"/row")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	// Idle → OOB group head should be present.
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Errorf("idle GET /feeds/{id}/row should include OOB group head, body=%s", body)
	}
	// The group head should reference the uncategorised group (catID=0).
	if !strings.Contains(body, `id="feed-group-0"`) {
		t.Errorf("OOB group head should target feed-group-0 for uncategorised feed, body=%s", body)
	}
}

// An unknown feed (e.g. deleted out-of-band while a row was still polling) must
// return 200 with an empty body, not 404: htmx's outerHTML swap then removes the
// row and the poll stops. A 404 would not swap, leaving the row polling forever.
func TestFeedRowForUnknownFeedReturnsEmpty200(t *testing.T) {
	h, _ := newTestHandler(t, coretest.StubFetcher{})
	rec := do(t, h, "GET", "/feeds/99999/row")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown feed, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "" {
		t.Fatalf("expected empty body for unknown feed (so htmx removes the row), got %q", body)
	}
}
