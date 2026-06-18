package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "https://bfeed.marlin-tet.ts.net")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ListenAddr != ":8080" || c.DatabasePath != "./bfeed.db" {
		t.Fatalf("defaults wrong: %+v", c)
	}
	if c.PollInterval != 15*time.Minute || c.FeedWorkers != 20 || c.HostConcurrency != 3 {
		t.Fatalf("poll defaults wrong: %+v", c)
	}
}

func TestLoadRequiresBaseURL(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when BFEED_BASE_URL unset")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "https://x.test")
	t.Setenv("BFEED_POLL_INTERVAL", "5m")
	t.Setenv("BFEED_FEED_WORKERS", "8")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval != 5*time.Minute || c.FeedWorkers != 8 {
		t.Fatalf("overrides not applied: %+v", c)
	}
}
