package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr        string
	BaseURL           string
	DatabasePath      string
	LogLevel          string
	LogFormat         string
	PollTick          time.Duration
	PollInterval      time.Duration
	MaxBackoff        time.Duration
	FeedWorkers       int
	BatchSize         int
	HostConcurrency   int
	ScrapeWorkers     int
	ScrapeTick        time.Duration
	ScrapeBatch       int
	ScrapeMaxAttempts int
}

func Load() (Config, error) {
	c := Config{
		ListenAddr:        env("BFEED_LISTEN_ADDR", ":8080"),
		BaseURL:           env("BFEED_BASE_URL", ""),
		DatabasePath:      env("BFEED_DATABASE_PATH", "./bfeed.db"),
		LogLevel:          env("BFEED_LOG_LEVEL", "info"),
		LogFormat:         env("BFEED_LOG_FORMAT", "json"),
		PollTick:          envDur("BFEED_POLL_TICK", time.Minute),
		PollInterval:      envDur("BFEED_POLL_INTERVAL", 15*time.Minute),
		MaxBackoff:        envDur("BFEED_MAX_BACKOFF", 24*time.Hour),
		FeedWorkers:       envInt("BFEED_FEED_WORKERS", 20),
		BatchSize:         envInt("BFEED_BATCH_SIZE", 100),
		HostConcurrency:   envInt("BFEED_HOST_CONCURRENCY", 3),
		ScrapeWorkers:     envInt("BFEED_SCRAPE_WORKERS", 20),
		ScrapeTick:        envDur("BFEED_SCRAPE_TICK", time.Minute),
		ScrapeBatch:       envInt("BFEED_SCRAPE_BATCH", 50),
		ScrapeMaxAttempts: envInt("BFEED_SCRAPE_MAX_ATTEMPTS", 3),
	}
	if c.BaseURL == "" {
		return c, fmt.Errorf("BFEED_BASE_URL is required")
	}
	if c.FeedWorkers < 1 || c.HostConcurrency < 1 {
		return c, fmt.Errorf("worker/host-concurrency must be >= 1")
	}
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

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
