package config

import (
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr           string
	BaseURL              string
	DatabasePath         string
	LogLevel             string
	LogFormat            string
	PollTick             time.Duration
	SchedMinInterval     time.Duration
	SchedMaxInterval     time.Duration
	SchedFactor          float64
	FeedErrorLimit       int
	MaxBackoff           time.Duration
	FeedWorkers          int
	BatchSize            int
	HostConcurrency      int
	ScrapeWorkers        int
	ScrapeTick           time.Duration
	ScrapeBatch          int
	ScrapeMaxAttempts    int
	ImageProxy           bool
	ImageProxySecret     string
	BlockPrivateNetworks bool
	AllowPrivateCIDRs    []netip.Prefix
}

// LoadMinimal reads only the handful of settings the migrate and healthcheck
// subcommands actually use (all plain strings, no parsing), deliberately
// skipping BaseURL requirement and every poller/scraper knob. This keeps those
// subcommands working when BFEED_BASE_URL is unset and prevents a malformed
// poller variable from making a liveness probe report unhealthy. It cannot fail.
func LoadMinimal() Config {
	return Config{
		ListenAddr:   env("BFEED_LISTEN_ADDR", ":8080"),
		DatabasePath: env("BFEED_DATABASE_PATH", "./bfeed.db"),
		LogLevel:     env("BFEED_LOG_LEVEL", "info"),
		LogFormat:    env("BFEED_LOG_FORMAT", "json"),
	}
}

func Load() (Config, error) {
	var l loader
	c := Config{
		ListenAddr:           env("BFEED_LISTEN_ADDR", ":8080"),
		BaseURL:              env("BFEED_BASE_URL", ""),
		DatabasePath:         env("BFEED_DATABASE_PATH", "./bfeed.db"),
		LogLevel:             env("BFEED_LOG_LEVEL", "info"),
		LogFormat:            env("BFEED_LOG_FORMAT", "json"),
		PollTick:             l.dur("BFEED_POLL_TICK", time.Minute),
		SchedMinInterval:     l.dur("BFEED_SCHED_MIN_INTERVAL", 5*time.Minute),
		SchedMaxInterval:     l.dur("BFEED_SCHED_MAX_INTERVAL", 24*time.Hour),
		SchedFactor:          l.float("BFEED_SCHED_FACTOR", 1.0),
		FeedErrorLimit:       l.int("BFEED_FEED_ERROR_LIMIT", 20),
		MaxBackoff:           l.dur("BFEED_MAX_BACKOFF", 24*time.Hour),
		FeedWorkers:          l.int("BFEED_FEED_WORKERS", 20),
		BatchSize:            l.int("BFEED_BATCH_SIZE", 100),
		HostConcurrency:      l.int("BFEED_HOST_CONCURRENCY", 3),
		ScrapeWorkers:        l.int("BFEED_SCRAPE_WORKERS", 20),
		ScrapeTick:           l.dur("BFEED_SCRAPE_TICK", time.Minute),
		ScrapeBatch:          l.int("BFEED_SCRAPE_BATCH", 50),
		ScrapeMaxAttempts:    l.int("BFEED_SCRAPE_MAX_ATTEMPTS", 3),
		ImageProxy:           l.boolean("BFEED_IMAGE_PROXY", true),
		ImageProxySecret:     env("BFEED_IMAGE_PROXY_SECRET", ""),
		BlockPrivateNetworks: l.boolean("BFEED_BLOCK_PRIVATE_NETWORKS", true),
	}
	// A set-but-unparseable value is a misconfiguration: fail fast naming the
	// offending variable rather than silently starting on the built-in default.
	if err := l.err(); err != nil {
		return c, err
	}
	if c.BaseURL == "" {
		return c, fmt.Errorf("BFEED_BASE_URL is required")
	}
	// Must be an absolute http(s) URL with a host: it seeds external links and,
	// critically, the DNS-rebinding Host guard (web.hostGuard) — a scheme-less
	// value like "host:8080" parses with an empty Host and would silently
	// disable that guard, so reject it here rather than fail open at runtime.
	if u, err := url.Parse(c.BaseURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return c, fmt.Errorf("BFEED_BASE_URL must be an absolute http(s) URL with a host (e.g. https://bfeed.example)")
	}
	if c.FeedWorkers < 1 || c.HostConcurrency < 1 {
		return c, fmt.Errorf("worker/host-concurrency must be >= 1")
	}
	if c.SchedMinInterval <= 0 || c.SchedMinInterval >= c.SchedMaxInterval {
		return c, fmt.Errorf("BFEED_SCHED_MIN_INTERVAL must be > 0 and < BFEED_SCHED_MAX_INTERVAL")
	}
	// !(>0) rejects NaN (a malformed "NaN" parses as a valid float) as well as
	// zero/negative; IsInf rejects +Inf, which would slip past the > 0 check.
	if !(c.SchedFactor > 0) || math.IsInf(c.SchedFactor, 0) {
		return c, fmt.Errorf("BFEED_SCHED_FACTOR must be a finite number > 0")
	}
	if c.FeedErrorLimit < 1 {
		return c, fmt.Errorf("BFEED_FEED_ERROR_LIMIT must be >= 1")
	}
	cidrs, err := parseCIDRs(os.Getenv("BFEED_ALLOW_PRIVATE_CIDRS"))
	if err != nil {
		return c, fmt.Errorf("BFEED_ALLOW_PRIVATE_CIDRS: %w", err)
	}
	c.AllowPrivateCIDRs = cidrs
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// loader accumulates parse errors across the typed env lookups so Load can
// report every malformed variable at once. Each helper returns the default on a
// bad value (so the returned Config is still fully populated for context) but
// records an error, which Load surfaces via err() before it ever validates.
type loader struct{ errs []error }

func (l *loader) err() error { return errors.Join(l.errs...) }

func (l *loader) int(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid integer %q", k, v))
		return def
	}
	return n
}

func (l *loader) float(k string, def float64) float64 {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid number %q", k, v))
		return def
	}
	return f
}

func (l *loader) dur(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid duration %q (want e.g. 30s, 15m, 24h)", k, v))
		return def
	}
	return d
}

func (l *loader) boolean(k string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "":
		return def
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	default:
		l.errs = append(l.errs, fmt.Errorf("%s: invalid boolean %q (want true/false/on/off/1/0)", k, os.Getenv(k)))
		return def
	}
}

func parseCIDRs(v string) ([]netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", part, err)
		}
		out = append(out, p.Masked()) // canonical form (host bits cleared)
	}
	return out, nil
}
