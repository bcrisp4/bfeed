package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFontServedWithImmutableCache(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/static/fonts/literata-400.woff2", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("font status %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("font Cache-Control = %q, want immutable", cc)
	}
}

func TestCSSAndJSGetShortRevalidatingCache(t *testing.T) {
	h, _ := newWeb(t)
	for _, path := range []string{"/static/app.css", "/static/htmx.min.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s status %d", path, rec.Code)
		}
		cc := rec.Header().Get("Cache-Control")
		if !strings.Contains(cc, "max-age=3600") {
			t.Fatalf("%s Cache-Control = %q, want max-age=3600", path, cc)
		}
		if strings.Contains(cc, "immutable") {
			t.Fatalf("%s must NOT be immutable (changes between releases): %q", path, cc)
		}
	}
}
