package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChromeForDefaultsAndEnums(t *testing.T) {
	h := &Handler{}
	mk := func(cookies map[string]string) chrome {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for k, v := range cookies {
			r.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		return h.chromeFor(r, "unread")
	}
	if c := mk(nil); c.Theme != "" || c.Summaries != "show" || c.Width != "comfortable" || c.Active != "unread" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if c := mk(map[string]string{"bfeed_theme": "system"}); c.Theme != "" {
		t.Fatalf("system should map to empty theme, got %q", c.Theme)
	}
	if c := mk(map[string]string{"bfeed_theme": "sepia", "bfeed_summary": "hide", "bfeed_width": "wide"}); c.Theme != "sepia" || c.Summaries != "hide" || c.Width != "wide" {
		t.Fatalf("valid values not honoured: %+v", c)
	}
	if c := mk(map[string]string{"bfeed_summary": "bogus", "bfeed_width": "bogus"}); c.Summaries != "show" || c.Width != "comfortable" {
		t.Fatalf("unknown values must fall back: %+v", c)
	}
}
