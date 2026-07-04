package imgproxy_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/imgproxy"
)

// maxImageBytes mirrors the handler's streaming cap.
const maxImageBytes = 10 << 20

func newHandler(f imgproxy.StreamFetcher) *imgproxy.Handler {
	return imgproxy.New(f, imgproxy.NewSigner([]byte("k")), coretest.DiscardLogger())
}

func TestHandlerMissingParams(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(coretest.StubStreamFetcher{}).ServeHTTP(rec, httptest.NewRequest("GET", "/img", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerRejectsBadSig(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/img?u=https%3A%2F%2Fx.com%2Fa.jpg&s=deadbeef", nil)
	newHandler(coretest.StubStreamFetcher{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerBadScheme(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	rec := httptest.NewRecorder()
	newHandler(coretest.StubStreamFetcher{}).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("ftp://x.com/a"), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerNonImageContentType(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	f := coretest.StubStreamFetcher{Status: 200, ContentType: "text/html", Body: "<html>", ContentLength: 6}
	rec := httptest.NewRecorder()
	newHandler(f).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("https://x.com/a"), nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerServesImage(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	closes := 0
	f := coretest.StubStreamFetcher{Status: 200, ContentType: "image/png", Body: "PNGDATA", ContentLength: 7, Closes: &closes}
	rec := httptest.NewRecorder()
	newHandler(f).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("https://x.com/a.png"), nil))
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") == "" {
		t.Fatal("missing Cache-Control")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing Content-Security-Policy")
	}
	if cl := rec.Header().Get("Content-Length"); cl != "7" {
		t.Fatalf("Content-Length=%q, want 7", cl)
	}
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "PNGDATA" {
		t.Fatalf("body=%q", b)
	}
	if closes != 1 {
		t.Fatalf("body Close called %d times, want exactly 1 (token release)", closes)
	}
}

// F2: an upstream that declares a length over the cap is rejected before any body
// is written — a partial image must never reach the client with an immutable cache.
func TestHandlerRejectsOversizeContentLength(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	f := coretest.StubStreamFetcher{Status: 200, ContentType: "image/png", Body: "x", ContentLength: maxImageBytes + 1}
	rec := httptest.NewRecorder()
	newHandler(f).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("https://x.com/big.png"), nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code=%d, want 502", rec.Code)
	}
	if rec.Body.Len() != 0 && strings.Contains(rec.Body.String(), "x") {
		t.Fatalf("truncated image body was written: %q", rec.Body.String())
	}
}

// F2: a body that streams past the cap with no declared length must abort the
// response (panic http.ErrAbortHandler) rather than complete a truncated 200.
func TestHandlerAbortsOnOversizeStream(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	f := coretest.StubStreamFetcher{Status: 200, ContentType: "image/png", Body: strings.Repeat("x", maxImageBytes+100), ContentLength: -1}
	rec := httptest.NewRecorder()
	defer func() {
		rec := recover()
		if err, ok := rec.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("expected http.ErrAbortHandler panic, got %v", rec)
		}
	}()
	newHandler(f).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("https://x.com/big.png"), nil))
	t.Fatal("handler returned normally; expected abort on oversize stream")
}
