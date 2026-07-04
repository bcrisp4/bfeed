package web_test

import (
	"context"
	"errors"
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

// newWebOver builds the handler over a caller-supplied store so a test can wrap
// MemStore to inject store errors on specific methods (audit B10 error paths).
func newWebOver(t *testing.T, store core.Store) http.Handler {
	t.Helper()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	return web.New(fs, es, cs, ss, log, nil, nil, 20, "")
}

var errDB = errors.New("db unavailable")

// B10 #10: a non-ErrNotFound store error on a lookup must surface as 500, not 404
// (a transient DB failure masquerading as "not found" is misleading + undiagnosable).
type getEntryErrStore struct{ *coretest.MemStore }

func (getEntryErrStore) GetEntry(context.Context, core.ID, core.ID) (*core.Entry, error) {
	return nil, errDB
}

func TestEntryStoreErrorIs500Not404(t *testing.T) {
	h := newWebOver(t, getEntryErrStore{coretest.NewMemStore()})
	req := httptest.NewRequest(http.MethodGet, "/entries/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("transient store error: want 500, got %d", rec.Code)
	}
}

// B10 #9: a failed delete must not reply success — the row would vanish while the
// entry still exists. Return 500.
type deleteEntryErrStore struct{ *coretest.MemStore }

func (deleteEntryErrStore) DeleteEntry(context.Context, core.ID, core.ID) error { return errDB }

func TestDeleteEntryStoreErrorIs500(t *testing.T) {
	inner := coretest.NewMemStore()
	h := newWebOver(t, deleteEntryErrStore{inner})
	ctx := context.Background()
	fid, _ := inner.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := inner.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodPost, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10)+"/delete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed delete: want 500, got %d", rec.Code)
	}
}

// B10 review: deleting an already-gone entry (double-submit, or a stale list row
// removed in another tab) is idempotent success — ErrNotFound must NOT become 500,
// or htmx won't apply hx-swap="delete" and the row is stranded on screen.
func TestDeleteEntryNotFoundIsSuccess(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})
	path := "/entries/" + strconv.FormatInt(int64(ins[0].ID), 10) + "/delete"

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, path, nil))
	if rec1.Code != 200 {
		t.Fatalf("first delete: want 200, got %d", rec1.Code)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, path, nil))
	if rec2.Code != 200 {
		t.Fatalf("second (idempotent) delete: want 200, got %d", rec2.Code)
	}
}

// B10 #8: a transient EntryStats failure must hide counts on the per-row fragment
// (mirroring listFeeds), not render a fabricated "0 unread".
type statsErrStore struct{ *coretest.MemStore }

func (statsErrStore) EntryStatsByFeed(context.Context, core.ID) (map[core.ID]core.FeedEntryStats, error) {
	return nil, errDB
}

func TestFeedEditFormHidesCountsOnStatsError(t *testing.T) {
	inner := coretest.NewMemStore()
	h := newWebOver(t, statsErrStore{inner})
	ctx := context.Background()
	fid, _ := inner.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})

	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(int64(fid), 10)+"/edit", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("edit form status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "unread") {
		t.Fatalf("stats error still rendered fabricated counts:\n%s", rec.Body.String())
	}
}

// B10 #7: the reader surfaces a note when full-content extraction terminally failed.
func TestReaderShowsExtractionFailureNote(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	id := coretest.SeedEntry(store, &core.Entry{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P", Content: "<p>feed body</p>",
		Status: core.StatusUnread, PublishedAt: time.Unix(100, 0), ExtractState: core.ExtractFailed, ExtractError: "status 503 content-type \"text/html\"",
	})

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(id), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("reader status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Full-text extraction failed") {
		t.Fatalf("reader missing extraction-failure note:\n%s", rec.Body.String())
	}
}
