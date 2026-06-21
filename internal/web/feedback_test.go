package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
