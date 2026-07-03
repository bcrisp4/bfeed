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
	"strings"
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
	sems map[string]*hostSem
}

// hostSem is a per-host concurrency limiter plus a reference count. The count
// lets us evict the map entry once no fetch is holding or waiting on it, so the
// map bounds to the set of currently-active hosts rather than every host ever
// fetched — the keys are attacker-influenced (entry/img URLs from feed content),
// so an unbounded map is externally drivable memory growth.
type hostSem struct {
	ch   chan struct{}
	refs int
}

func New(cfg Config) *Client {
	if cfg.HostConcurrency <= 0 {
		cfg.HostConcurrency = 3
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 10 << 20
	}
	if cfg.Timeout <= 0 {
		// A zero Config.Timeout yields http.Client{Timeout: 0} (no deadline), so a
		// stalled server could pin a poller/scraper worker slot and its per-host
		// token forever. Default it, matching the other field back-fills above.
		cfg.Timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if cfg.BlockPrivateNetworks {
		// The SSRF guard inspects the *destination* IP in the dialer's Control
		// hook. An egress proxy would make the transport dial the proxy instead,
		// so Control would only see the proxy's address and the destination filter
		// would be silently bypassed. The guard and HTTP(S)_PROXY are therefore
		// mutually exclusive: while guarding, leave Proxy nil and dial directly so
		// Control stays authoritative.
		dialer.Control = guardDial(cfg.AllowedCIDRs)
	} else {
		// Guard disabled: restore the standard egress-proxy behaviour
		// (HTTP_PROXY/HTTPS_PROXY/NO_PROXY) that http.DefaultTransport provides.
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: checkRedirect(5), Transport: tr},
		sems: make(map[string]*hostSem),
	}
}

// redirectTracker accumulates redirect-chain facts for a single Fetch. It lives
// in the request context because http.Client's CheckRedirect closure is shared
// across all requests on the Client, so per-request state can't live on Client.
type redirectTracker struct {
	hops      int
	permanent bool // starts true; cleared if any hop is not 301/308
}

type redirectKey struct{}

var _ core.Fetcher = (*Client)(nil)

// acquire blocks until a per-host concurrency token for key is available (or ctx
// is done), then returns a release func. The map entry is reference-counted: it
// is created on first waiter and deleted once the last holder releases, so
// c.sems never accumulates entries for hosts with no in-flight fetch.
func (c *Client) acquire(ctx context.Context, key string) (func(), error) {
	c.mu.Lock()
	s, ok := c.sems[key]
	if !ok {
		s = &hostSem{ch: make(chan struct{}, c.cfg.HostConcurrency)}
		c.sems[key] = s
	}
	s.refs++
	c.mu.Unlock()

	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch; c.unref(key, s) }, nil
	case <-ctx.Done():
		// Never took a token; undo the ref without draining the channel.
		c.unref(key, s)
		return nil, ctx.Err()
	}
}

// unref drops a reference to the per-host sem and evicts the map entry once no
// fetch holds or waits on it. Callers that took a token must drain it first.
func (c *Client) unref(key string, s *hostSem) {
	c.mu.Lock()
	s.refs--
	if s.refs == 0 {
		delete(c.sems, key)
	}
	c.mu.Unlock()
}

func (c *Client) Fetch(ctx context.Context, req core.FetchRequest) (*core.FetchResponse, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("bad url: %w", err)
	}
	// The token is acquired once, before Do, for the ORIGIN host. A fetch that
	// redirects to another host therefore still counts against the origin host's
	// budget rather than the target's — a known residual (LOW, single-user MVP);
	// swapping tokens mid-redirect in CheckRedirect would add deadlock surface for
	// little gain. Normalizing the key (hostKey) at least stops case/default-port
	// variants of the SAME host getting independent budgets.
	release, err := c.acquire(ctx, hostKey(u))
	if err != nil {
		return nil, err
	}
	defer release()

	rt := &redirectTracker{permanent: true}
	ctx = context.WithValue(ctx, redirectKey{}, rt)
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
		FinalURL:     resp.Request.URL.String(), // last request in the redirect chain
	}
	out.PermanentRedirect = rt.hops > 0 && rt.permanent
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode == http.StatusOK {
		// Read one byte past the cap so we can tell "exactly MaxBytes" from
		// "larger than MaxBytes": a body over the cap is rejected rather than
		// silently truncated and treated as complete (which would persist a
		// cut-off article / serve a broken image with a year-long cache).
		body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		if int64(len(body)) > c.cfg.MaxBytes {
			return nil, fmt.Errorf("response body exceeds %d bytes", c.cfg.MaxBytes)
		}
		out.Body = body
		return out, nil
	}
	// Drain body on non-200/304 responses so net/http can reuse the TCP connection.
	io.Copy(io.Discard, io.LimitReader(resp.Body, c.cfg.MaxBytes)) //nolint:errcheck,gosec // draining body for conn reuse
	return out, nil
}

// maxRetryAfter caps a server-supplied Retry-After. Feed servers are untrusted,
// so an uncapped value could park a feed far in the future (bypassing backoff)
// and, for large numeric values, overflow the int64 nanosecond multiply into a
// garbage duration. Callers additionally clamp to their own MaxBackoff.
const maxRetryAfter = 24 * time.Hour

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		// Cap before multiplying so an absurd value can't overflow the duration.
		if secs > int(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, maxRetryAfter)
		}
	}
	return 0
}

// checkRedirect caps the chain at n hops and records, in the request-scoped
// redirectTracker, whether every hop so far was a permanent (301/308) redirect —
// so the caller can tell a moved feed (adopt the new URL) from a temporary one.
func checkRedirect(n int) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= n {
			return fmt.Errorf("stopped after %d redirects", n)
		}
		if rt, ok := req.Context().Value(redirectKey{}).(*redirectTracker); ok {
			rt.hops++
			// req.Response is the redirect response that produced this request.
			if req.Response == nil ||
				(req.Response.StatusCode != http.StatusMovedPermanently &&
					req.Response.StatusCode != http.StatusPermanentRedirect) {
				rt.permanent = false
			}
		}
		return nil
	}
}

// hostKey normalizes a URL's host for the per-host politeness semaphore:
// lower-cased hostname with the port dropped when it's the scheme default
// (:80 for http, :443 for https). Without this, "Example.com", "example.com",
// and "example.com:80" would each get an independent concurrency budget.
func hostKey(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" || isDefaultPort(u.Scheme, port) {
		return host
	}
	return net.JoinHostPort(host, port)
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

// blockedPrefixes are non-public ranges netip's Is* predicates miss: CGNAT
// (RFC 6598, used by Tailscale); 0.0.0.0/8 "this network" (RFC 1122, which can
// route to the local host); reserved Class E (RFC 1112); the 6to4 (RFC 3056)
// and NAT64 (RFC 6052) prefixes, which embed an IPv4 address and so can encode a
// private/loopback target behind an IPv6 address on a host with such a gateway;
// benchmarking (RFC 2544, deployed as internal space on some networks); IETF
// protocol assignments incl. DS-Lite AFTR (RFC 7335); the deprecated 6to4 relay
// anycast; deprecated IPv6 site-local (still routable on legacy internal nets);
// and Teredo (RFC 4380), which like 6to4/NAT64 embeds an IPv4 address.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("2001::/32"),
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
