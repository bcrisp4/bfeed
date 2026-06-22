package fetch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

type Config struct {
	UserAgent       string
	HostConcurrency int
	Timeout         time.Duration
	MaxBytes        int64
	// BlockPrivateNetworks rejects connections to private/loopback/link-local/
	// metadata IPs (SSRF guard). AllowedCIDRs re-permits specific ranges.
	BlockPrivateNetworks bool
	AllowedCIDRs         []netip.Prefix
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
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if cfg.BlockPrivateNetworks {
		dialer.Control = guardDial(cfg.AllowedCIDRs)
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: capRedirects(5), Transport: tr},
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

// blockedPrefixes are non-public ranges netip's Is* predicates miss: CGNAT
// (RFC 6598, used by Tailscale); 0.0.0.0/8 "this network" (RFC 1122, which can
// route to the local host); reserved Class E (RFC 1112); and the 6to4 (RFC 3056)
// and NAT64 (RFC 6052) prefixes, which embed an IPv4 address and so can encode a
// private/loopback target behind an IPv6 address on a host with such a gateway.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// isBlockedIP reports whether ip is not safely public (SSRF target).
func isBlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() ||
		ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsPrivate() || // RFC1918 + ULA fc00::/7
		ip.IsLinkLocalUnicast() || // 169.254.0.0/16 (incl. 169.254.169.254 metadata) + fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// guardDial returns a net.Dialer Control hook that runs once per dialled address
// after DNS resolution (so it also covers redirect targets and defeats
// DNS-rebind TOCTOU). It rejects blocked IPs unless they fall in an allowed CIDR.
func guardDial(allowed []netip.Prefix) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("ssrf guard: bad address %q: %w", address, err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("ssrf guard: unparseable ip %q: %w", host, err)
		}
		ip = ip.Unmap()
		for _, p := range allowed {
			if p.Contains(ip) {
				return nil
			}
		}
		if isBlockedIP(ip) {
			return fmt.Errorf("ssrf guard: blocked address %s", ip)
		}
		return nil
	}
}
