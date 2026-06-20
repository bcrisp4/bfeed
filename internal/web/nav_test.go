package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNavHasPrimaryAndOverflow(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`class="bottombar"`, `class="topnav"`, `class="more-sheet"`,
		`href="/settings"`, `href="/history"`, `href="/categories"`, `href="/search"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("nav missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `aria-current="page"`) {
		t.Fatalf("active nav item not marked:\n%s", body)
	}
}
