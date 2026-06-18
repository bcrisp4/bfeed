package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

type Config struct {
	UserAgent       string
	HostConcurrency int
	Timeout         time.Duration
	MaxBytes        int64
}

type Client struct {
	cfg  Config
	http *http.Client
	mu   sync.Mutex
	sems map[string]chan struct{}
}

func New(cfg Config) *Client {
	if cfg.HostConcurrency <= 0 {
		cfg.HostConcurrency = 3
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 10 << 20
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: capRedirects(5)},
		sems: make(map[string]chan struct{}),
	}
}

var _ core.Fetcher = (*Client)(nil)

func (c *Client) sem(host string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sems[host]
	if !ok {
		s = make(chan struct{}, c.cfg.HostConcurrency)
		c.sems[host] = s
	}
	return s
}

func (c *Client) Fetch(ctx context.Context, req core.FetchRequest) (*core.FetchResponse, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("bad url: %w", err)
	}
	sem := c.sem(u.Host)
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("User-Agent", c.cfg.UserAgent)
	if req.ETag != "" {
		hreq.Header.Set("If-None-Match", req.ETag)
	}
	if req.LastModified != "" {
		hreq.Header.Set("If-Modified-Since", req.LastModified)
	}

	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	out := &core.FetchResponse{
		Status:       resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		RetryAfter:   parseRetryAfter(resp.Header.Get("Retry-After")),
	}
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxBytes))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		out.Body = body
		return out, nil
	}
	// Drain body on non-200/304 responses so net/http can reuse the TCP connection.
	io.Copy(io.Discard, io.LimitReader(resp.Body, c.cfg.MaxBytes)) //nolint:errcheck,gosec // draining body for conn reuse
	return out, nil
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func capRedirects(n int) func(*http.Request, []*http.Request) error {
	return func(_ *http.Request, via []*http.Request) error {
		if len(via) >= n {
			return fmt.Errorf("stopped after %d redirects", n)
		}
		return nil
	}
}
