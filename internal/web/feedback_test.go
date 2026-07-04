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
	// The bfcache reload guard is externalised to /static/app.js (so the page
	// CSP can use script-src 'self'): the layout must reference it, and the
	// asset must carry the pageshow/persisted reload.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "app.js") {
		t.Fatalf("layout does not reference app.js:\n%s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	js := rec.Body.String()
	if !strings.Contains(js, "pageshow") || !strings.Contains(js, "persisted") {
		t.Fatalf("app.js missing pageshow/persisted bfcache reload guard:\n%s", js)
	}
}

func TestSecurityHeadersOnDynamicHTML(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	wants := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, want := range wants {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, frag := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, frag) {
			t.Errorf("CSP missing %q: %s", frag, csp)
		}
	}
}

func TestHostGuardRejectsForeignHost(t *testing.T) {
	h, _ := newWebHost(t, "bfeed.example:8080")

	// Matching host passes; case-insensitively (hostnames are case-insensitive).
	for _, host := range []string{"bfeed.example:8080", "BFEED.example:8080"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Host %q: status %d, want 200", host, rec.Code)
		}
	}

	// Foreign host (DNS-rebinding attacker) is rejected — and so is a spoofed
	// loopback Host on a non-healthz endpoint (same-machine attacker).
	for _, host := range []string{"evil.example", "127.0.0.1:9999", "localhost:8080"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMisdirectedRequest {
			t.Fatalf("foreign Host %q: status %d, want 421", host, rec.Code)
		}
	}

	// The /healthz path is exempt so the container HEALTHCHECK survives.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "127.0.0.1:9999"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz via loopback: status %d, want 200", rec.Code)
	}
}

func TestHostGuardToleratesDefaultPort(t *testing.T) {
	// BaseURL host without an explicit port; a client that includes the default
	// port (or omits it) must still be accepted.
	h, _ := newWebHost(t, "bfeed.example")
	for _, host := range []string{"bfeed.example", "bfeed.example:80", "bfeed.example:443"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q vs expected bfeed.example: status %d, want 200", host, rec.Code)
		}
	}
	// A non-default port is still a distinct authority → rejected.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "bfeed.example:8443"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("Host with non-default port: status %d, want 421", rec.Code)
	}
}

func TestHostGuardDisabledWhenEmpty(t *testing.T) {
	h, _ := newWeb(t) // expectedHost ""
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "anything.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("guard should be disabled: status %d, want 200", rec.Code)
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
	if !strings.Contains(body, `aria-label="Unstar"`) || !strings.Contains(body, `class="act star on"`) {
		t.Fatalf("reader star did not return the toggled star button:\n%s", body)
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "<body") {
		t.Fatalf("reader star response should be the button fragment, not a full page:\n%s", body)
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
