package web_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The reader page wraps its content in <div class="reader"> — the scoping
// hook for the full-bleed CSS (docs/superpowers/specs/2026-07-04-full-bleed-reader-design.md).
func TestReaderPageHasReaderWrapper(t *testing.T) {
	h, store := newWeb(t)
	_, eid := seedFeedWithEntry(t, store, "g-reader", "Post")

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(eid), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div class="reader">`) {
		t.Fatal("reader page missing <div class=\"reader\"> wrapper")
	}
	if !strings.Contains(body, `class="article"`) {
		t.Fatal("reader page missing article element")
	}
}
