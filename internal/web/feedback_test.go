package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func TestDynamicHTMLIsNoStore(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("dynamic HTML Cache-Control = %q, want no-store", got)
	}
}

func TestStaticAssetsKeepTheirCacheHeader(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("static status %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "no-store") || got == "" {
		t.Fatalf("static Cache-Control = %q, want the cacheStatic max-age value", got)
	}
}

func TestLayoutHasBfcacheReloadScript(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "pageshow") || !strings.Contains(body, "persisted") {
		t.Fatalf("layout missing pageshow/persisted bfcache reload guard:\n%s", body)
	}
}

func TestEntryRowHasIconActions(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "Hello", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`class="actbar"`, `aria-label="Mark read"`, `aria-label="Star"`, `aria-label="Delete"`, `hx-disabled-elt="this"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("entry row missing %q:\n%s", want, body)
		}
	}
}

func TestIconsRenderInBottomBar(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	// The bottom bar tabs must use inline SVG icons now, not text glyphs.
	if !strings.Contains(body, `class="bottombar"`) || !strings.Contains(body, `<span class="tab-ic" aria-hidden="true"><svg`) {
		t.Fatalf("bottom bar not using SVG icons:\n%s", body)
	}
}

func readerEntry(t *testing.T) (http.Handler, *coretest.MemStore, core.ID) {
	t.Helper()
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P", Content: "<p>body</p>", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})
	return h, store, ins[0].ID
}

func TestReaderRendersActions(t *testing.T) {
	h, _, id := readerEntry(t)
	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(id), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`class="readerbar"`, `id="reader-star"`, `aria-label="Mark unread"`, `aria-label="Delete"`, `Open original`} {
		if !strings.Contains(body, want) {
			t.Fatalf("reader missing %q:\n%s", want, body)
		}
	}
}

func TestReaderMarkUnreadRedirectsAndUnreads(t *testing.T) {
	h, store, id := readerEntry(t)
	// Open once so it is read, mimicking real use.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(id), 10), nil))

	form := strings.NewReader("from=reader")
	req := httptest.NewRequest(http.MethodPost, "/entries/"+strconv.FormatInt(int64(id), 10)+"/read", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("reader mark-unread HX-Redirect = %q, want /", rec.Header().Get("HX-Redirect"))
	}
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.Status != core.StatusUnread {
		t.Fatalf("entry not unread after reader mark-unread")
	}
}

func TestReaderStarReturnsStarFragment(t *testing.T) {
	h, store, id := readerEntry(t)
	form := strings.NewReader("from=reader")
	req := httptest.NewRequest(http.MethodPost, "/entries/"+strconv.FormatInt(int64(id), 10)+"/star", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `aria-label="Unstar"`) || !strings.Contains(body, "star on") {
		t.Fatalf("reader star did not return the toggled star button:\n%s", body)
	}
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if !got.Starred {
		t.Fatalf("entry not starred")
	}
}

func TestReaderDeleteRedirects(t *testing.T) {
	h, _, id := readerEntry(t)
	form := strings.NewReader("from=reader")
	req := httptest.NewRequest(http.MethodPost, "/entries/"+strconv.FormatInt(int64(id), 10)+"/delete", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("reader delete HX-Redirect = %q, want /", rec.Header().Get("HX-Redirect"))
	}
}

func TestSubscribeFailureShowsInlineError(t *testing.T) {
	h, _ := newWeb(t)
	// An URL with no scheme fails FeedService.Subscribe validation cleanly.
	form := strings.NewReader("url=notaurl")
	req := httptest.NewRequest(http.MethodPost, "/feeds", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscribe failure status %d, want 200", rec.Code)
	}
	if rec.Header().Get("HX-Refresh") == "true" {
		t.Fatalf("failed subscribe must not refresh")
	}
	if !strings.Contains(rec.Body.String(), `class="form-error"`) {
		t.Fatalf("missing inline error:\n%s", rec.Body.String())
	}
}
