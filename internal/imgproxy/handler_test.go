package imgproxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/imgproxy"
)

func newHandler(f core.Fetcher) *imgproxy.Handler {
	return imgproxy.New(f, imgproxy.NewSigner([]byte("k")), coretest.DiscardLogger())
}

func TestHandlerMissingParams(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(coretest.StubFetcher{}).ServeHTTP(rec, httptest.NewRequest("GET", "/img", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerRejectsBadSig(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/img?u=https%3A%2F%2Fx.com%2Fa.jpg&s=deadbeef", nil)
	newHandler(coretest.StubFetcher{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerBadScheme(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	rec := httptest.NewRecorder()
	newHandler(coretest.StubFetcher{}).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("ftp://x.com/a"), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerNonImageContentType(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	f := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html", Body: []byte("<html>")}}
	rec := httptest.NewRecorder()
	newHandler(f).ServeHTTP(rec, httptest.NewRequest("GET", s.ProxyURL("https://x.com/a"), nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandlerServesImage(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	f := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "image/png", Body: []byte("PNGDATA")}}
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
	b, _ := io.ReadAll(rec.Body)
	if string(b) != "PNGDATA" {
		t.Fatalf("body=%q", b)
	}
}
