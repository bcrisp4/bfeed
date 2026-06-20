package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThemeCookieDrivesDataAttr(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "bfeed_theme", Value: "dark"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `data-theme="dark"`) {
		t.Fatalf("expected data-theme=dark on <html>:\n%s", rec.Body.String())
	}
}

func TestSystemThemeOmitsDataAttr(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no cookie
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "data-theme=") {
		t.Fatalf("system theme must omit data-theme:\n%s", rec.Body.String())
	}
}

func TestBadThemeCookieFallsBackToSystem(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "bfeed_theme", Value: "neon"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "data-theme=") {
		t.Fatalf("unknown theme must fall back to system (no attr):\n%s", rec.Body.String())
	}
}

func TestSummaryCookieDrivesDataAttr(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "bfeed_summary", Value: "hide"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `data-summaries="hide"`) {
		t.Fatalf("expected data-summaries=hide on <html>:\n%s", rec.Body.String())
	}
}
