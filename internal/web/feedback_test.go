package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
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
	if !strings.Contains(rec.Body.String(), "pageshow") {
		t.Fatalf("layout missing pageshow reload script:\n%s", rec.Body.String())
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
