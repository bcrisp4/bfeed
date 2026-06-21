package web_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// Fingerprinting: templates must reference CSS/JS by a cache-busting URL whose
// ?v= is the real 12-char content hash, so a changed asset gets a new URL and
// the browser can't serve a stale copy. Asserting the hash shape (and that two
// distinct files hash differently) proves the value is content-derived rather
// than a constant placeholder that would never bust.
func TestTemplatesReferenceFingerprintedAssets(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()

	re := regexp.MustCompile(`/static/(app\.css|htmx\.min\.js)\?v=([0-9a-f]+)`)
	hashes := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		hashes[m[1]] = m[2]
	}
	for _, name := range []string{"app.css", "htmx.min.js"} {
		got, ok := hashes[name]
		if !ok {
			t.Fatalf("layout missing fingerprinted %s:\n%s", name, body)
		}
		if len(got) != 12 {
			t.Fatalf("%s ?v=%q is not the 12-char content hash", name, got)
		}
	}
	if hashes["app.css"] == hashes["htmx.min.js"] {
		t.Fatalf("css and js share hash %q — fingerprint is not content-derived", hashes["app.css"])
	}
}

// A fingerprinted request (carries ?v=) is safe to cache forever: the hash
// changes when the bytes change, so the URL itself is the version.
func TestFingerprintedAssetIsImmutable(t *testing.T) {
	h, _ := newWeb(t)
	for _, path := range []string{"/static/app.css?v=abc123", "/static/htmx.min.js?v=abc123"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s status %d", path, rec.Code)
		}
		cc := rec.Header().Get("Cache-Control")
		if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
			t.Fatalf("%s Cache-Control = %q, want immutable year-long", path, cc)
		}
	}
}

// Text responses are compressed when the client advertises support; fonts
// (already-compressed woff2) are not re-compressed.
func TestHTMLIsCompressedWhenAccepted(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", v)
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("body is not valid gzip: %v", err)
	}
	plain, _ := io.ReadAll(gz)
	if !strings.Contains(string(plain), "<html") {
		t.Fatalf("decompressed body is not the page:\n%s", plain)
	}
}

// CSS is in the compressible allowlist too (and compresses well), so guard it
// separately from HTML — a regression dropping text/css from the allowlist
// would otherwise pass unnoticed.
func TestCSSIsCompressedWhenAccepted(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("CSS Content-Encoding = %q, want gzip", ce)
	}
	if _, err := gzip.NewReader(rec.Body); err != nil {
		t.Fatalf("CSS body is not valid gzip: %v", err)
	}
}

func TestFontIsNotRecompressed(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/static/fonts/literata-400.woff2", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("font status %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("woff2 should not be re-compressed, got Content-Encoding=%q", ce)
	}
}
