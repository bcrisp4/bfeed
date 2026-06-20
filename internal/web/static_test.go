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
