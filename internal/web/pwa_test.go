package web_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestManifestServed guards that the web app manifest is served with the correct
// MIME type (registered in assets.go — Go's table lacks .webmanifest), advertises
// a standalone display and a maskable icon (the two properties that make it
// installable to a home screen), and that every icon it lists actually resolves,
// catching manifest/asset drift.
func TestManifestServed(t *testing.T) {
	h, _ := newWeb(t)

	rec := do(t, h, http.MethodGet, "/static/manifest.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/manifest+json") {
		t.Fatalf("manifest Content-Type = %q, want application/manifest+json", ct)
	}

	var m struct {
		Name    string `json:"name"`
		Display string `json:"display"`
		Icons   []struct {
			Src     string `json:"src"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Name == "" {
		t.Fatalf("manifest has no name")
	}
	if m.Display != "standalone" {
		t.Fatalf("manifest display = %q, want standalone (home-screen install)", m.Display)
	}
	if len(m.Icons) == 0 {
		t.Fatalf("manifest advertises no icons")
	}
	var maskable bool
	for _, ic := range m.Icons {
		if strings.Contains(ic.Purpose, "maskable") {
			maskable = true
		}
		if r := do(t, h, http.MethodGet, ic.Src); r.Code != http.StatusOK {
			t.Fatalf("manifest icon %q status = %d, want 200", ic.Src, r.Code)
		}
	}
	if !maskable {
		t.Fatalf("manifest has no maskable icon (Android home-screen crop)")
	}
}

// TestIconAssetsServed guards that every icon referenced from the manifest or the
// layout <head> is embedded and served with the right content type.
func TestIconAssetsServed(t *testing.T) {
	h, _ := newWeb(t)
	cases := []struct{ path, wantType string }{
		{"/static/favicon.svg", "image/svg+xml"},
		{"/static/favicon-32.png", "image/png"},
		{"/static/apple-touch-icon.png", "image/png"},
		{"/static/icon-192.png", "image/png"},
		{"/static/icon-512.png", "image/png"},
		{"/static/icon-maskable-512.png", "image/png"},
	}
	for _, c := range cases {
		rec := do(t, h, http.MethodGet, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.wantType) {
			t.Fatalf("%s Content-Type = %q, want %s", c.path, ct, c.wantType)
		}
	}
}

// TestAppJSHasPullToRefresh guards that the pull-to-refresh gesture (issue #112)
// ships in the served app.js. iOS standalone mode disables Safari's built-in
// pull-to-refresh, so the app provides its own — gated to standalone display so
// it doesn't double up with the browser's own in normal mobile tabs. JS can't be
// exercised in Go, so this asserts the handler markers survive in the embedded
// asset (mirrors TestLayoutHasBfcacheReloadScript).
func TestAppJSHasPullToRefresh(t *testing.T) {
	h, _ := newWeb(t)
	rec := do(t, h, http.MethodGet, "/static/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("app.js status %d, want 200", rec.Code)
	}
	js := rec.Body.String()
	for _, want := range []string{"display-mode: standalone", "touchmove", "location.reload"} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js missing pull-to-refresh marker %q:\n%s", want, js)
		}
	}
}

// TestLayoutHasPWAHead guards that the layout advertises the manifest, icons and
// theme-color so browsers can offer add-to-home-screen (companion to
// TestLayoutHasBfcacheReloadScript).
func TestLayoutHasPWAHead(t *testing.T) {
	h, _ := newWeb(t)
	body := do(t, h, http.MethodGet, "/").Body.String()
	for _, want := range []string{
		`rel="manifest"`,
		`rel="apple-touch-icon"`,
		`name="theme-color"`,
		`manifest.webmanifest`,
		// viewport-fit=cover: without it env(safe-area-inset-*) resolves to 0 and
		// the standalone bottom bar sits flush on the iOS home indicator (#107).
		`viewport-fit=cover`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("layout <head> missing %q:\n%s", want, body)
		}
	}
}
