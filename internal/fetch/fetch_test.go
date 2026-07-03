package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func testClient() *Client {
	return New(Config{UserAgent: "bfeed-test", HostConcurrency: 2, Timeout: 5 * time.Second, MaxBytes: 1 << 20})
}

func TestConditionalGET304(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte("body"))
	}))
	defer srv.Close()
	c := testClient()
	resp, err := c.Fetch(context.Background(), core.FetchRequest{URL: srv.URL, ETag: `"v1"`})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !resp.NotModified {
		t.Fatalf("expected NotModified, got status %d", resp.Status)
	}
}

func TestFetchReturnsBodyAndValidators(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()
	resp, err := testClient().Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "hello" || resp.ETag != `"abc"` || resp.LastModified == "" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestRetryAfterParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	resp, err := testClient().Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter = %v", resp.RetryAfter)
	}
}

func TestFetchFinalURLNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("body"))
	}))
	defer srv.Close()
	resp, err := testClient().Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinalURL != srv.URL {
		t.Fatalf("FinalURL = %q, want %q", resp.FinalURL, srv.URL)
	}
	if resp.PermanentRedirect {
		t.Fatal("PermanentRedirect = true with no redirect")
	}
}

func TestFetchPermanentRedirectFinalURL(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("moved"))
	}))
	defer dest.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", dest.URL)
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer src.Close()
	resp, err := testClient().Fetch(context.Background(), core.FetchRequest{URL: src.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinalURL != dest.URL {
		t.Fatalf("FinalURL = %q, want %q", resp.FinalURL, dest.URL)
	}
	if !resp.PermanentRedirect {
		t.Fatal("PermanentRedirect = false after a 301 chain")
	}
}

func TestFetchTemporaryRedirectNotPermanent(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("moved"))
	}))
	defer dest.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", dest.URL)
		w.WriteHeader(http.StatusFound) // 302
	}))
	defer src.Close()
	resp, err := testClient().Fetch(context.Background(), core.FetchRequest{URL: src.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinalURL != dest.URL {
		t.Fatalf("FinalURL = %q, want %q", resp.FinalURL, dest.URL)
	}
	if resp.PermanentRedirect {
		t.Fatal("PermanentRedirect = true after a 302 (temporary) redirect")
	}
}

func TestHostKeyNormalization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://Example.com/feed", "example.com"},
		{"http://example.com:80/feed", "example.com"},
		{"https://example.com:443/feed", "example.com"},
		{"http://example.com:8080/feed", "example.com:8080"},
		{"https://EXAMPLE.com/feed", "example.com"},
	}
	for _, c := range cases {
		u, err := url.Parse(c.in)
		if err != nil {
			t.Fatalf("parse %q: %v", c.in, err)
		}
		if got := hostKey(u); got != c.want {
			t.Errorf("hostKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPerHostConcurrencyCap(t *testing.T) {
	var inflight, max int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inflight, 1)
		for {
			old := atomic.LoadInt32(&max)
			if n <= old || atomic.CompareAndSwapInt32(&max, old, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := New(Config{UserAgent: "t", HostConcurrency: 2, Timeout: 5 * time.Second, MaxBytes: 1 << 20})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&max) > 2 {
		t.Fatalf("per-host concurrency exceeded cap: peak=%d", max)
	}
}
