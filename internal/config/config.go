package config

import (
	"fmt"
	"math"
	"net/netip"
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

func Load() (Config, error) {
	c := Config{
		ListenAddr:           env("BFEED_LISTEN_ADDR", ":8080"),
		BaseURL:              env("BFEED_BASE_URL", ""),
		DatabasePath:         env("BFEED_DATABASE_PATH", "./bfeed.db"),
		LogLevel:             env("BFEED_LOG_LEVEL", "info"),
		LogFormat:            env("BFEED_LOG_FORMAT", "json"),
		PollTick:             envDur("BFEED_POLL_TICK", time.Minute),
		SchedMinInterval:     envDur("BFEED_SCHED_MIN_INTERVAL", 5*time.Minute),
		SchedMaxInterval:     envDur("BFEED_SCHED_MAX_INTERVAL", 24*time.Hour),
		SchedFactor:          envFloat("BFEED_SCHED_FACTOR", 1.0),
		FeedErrorLimit:       envInt("BFEED_FEED_ERROR_LIMIT", 20),
		MaxBackoff:           envDur("BFEED_MAX_BACKOFF", 24*time.Hour),
		FeedWorkers:          envInt("BFEED_FEED_WORKERS", 20),
		BatchSize:            envInt("BFEED_BATCH_SIZE", 100),
		HostConcurrency:      envInt("BFEED_HOST_CONCURRENCY", 3),
		ScrapeWorkers:        envInt("BFEED_SCRAPE_WORKERS", 20),
		ScrapeTick:           envDur("BFEED_SCRAPE_TICK", time.Minute),
		ScrapeBatch:          envInt("BFEED_SCRAPE_BATCH", 50),
		ScrapeMaxAttempts:    envInt("BFEED_SCRAPE_MAX_ATTEMPTS", 3),
		ImageProxy:           envBool("BFEED_IMAGE_PROXY", true),
		ImageProxySecret:     env("BFEED_IMAGE_PROXY_SECRET", ""),
		BlockPrivateNetworks: envBool("BFEED_BLOCK_PRIVATE_NETWORKS", true),
	}
	if c.BaseURL == "" {
		return c, fmt.Errorf("BFEED_BASE_URL is required")
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

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "":
		return def
	case "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	default:
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
