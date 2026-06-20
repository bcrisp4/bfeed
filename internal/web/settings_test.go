package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSettingsRendersCurrentTheme(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: "bfeed_theme", Value: "sepia"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Preferences") {
		t.Fatalf("settings page missing, code=%d body=%s", rec.Code, rec.Body.String())
	}
	// the sepia option is marked selected
	if !strings.Contains(rec.Body.String(), `value="sepia" checked`) {
		t.Fatalf("sepia not preselected:\n%s", rec.Body.String())
	}
}

func TestSettingsPostSetsCookies(t *testing.T) {
	h, _ := newWeb(t)
	form := strings.NewReader("theme=dark&summary=hide&width=wide")
	req := httptest.NewRequest(http.MethodPost, "/settings", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	got := map[string]string{}
	for _, c := range rec.Result().Cookies() {
		got[c.Name] = c.Value
	}
	if got["bfeed_theme"] != "dark" || got["bfeed_summary"] != "hide" || got["bfeed_width"] != "wide" {
		t.Fatalf("cookies not set: %+v", got)
	}
}

func TestSettingsPostRejectsBadEnum(t *testing.T) {
	h, _ := newWeb(t)
	form := strings.NewReader("theme=neon&summary=bogus&width=bogus")
	req := httptest.NewRequest(http.MethodPost, "/settings", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	got := map[string]string{}
	for _, c := range rec.Result().Cookies() {
		got[c.Name] = c.Value
	}
	if got["bfeed_theme"] != "system" || got["bfeed_summary"] != "show" || got["bfeed_width"] != "comfortable" {
		t.Fatalf("bad enums must fall back to defaults: %+v", got)
	}
}
