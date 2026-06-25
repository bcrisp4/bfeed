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
	if c.SchedMinInterval != 5*time.Minute || c.SchedMaxInterval != 24*time.Hour || c.FeedWorkers != 20 || c.HostConcurrency != 3 {
		t.Fatalf("poll defaults wrong: %+v", c)
	}
	if c.SchedFactor != 1.0 || c.FeedErrorLimit != 20 {
		t.Fatalf("sched factor/error-limit defaults wrong: %+v", c)
	}
	if c.ScrapeWorkers != 20 || c.ScrapeTick != time.Minute || c.ScrapeBatch != 50 || c.ScrapeMaxAttempts != 3 {
		t.Fatalf("scrape defaults wrong: %+v", c)
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
	t.Setenv("BFEED_SCHED_MIN_INTERVAL", "10m")
	t.Setenv("BFEED_FEED_WORKERS", "8")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SchedMinInterval != 10*time.Minute || c.FeedWorkers != 8 {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestSchedConfigValidation(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "http://x")
	t.Setenv("BFEED_SCHED_MIN_INTERVAL", "1h")
	t.Setenv("BFEED_SCHED_MAX_INTERVAL", "30m") // min >= max -> error
	if _, err := Load(); err == nil {
		t.Fatal("expected error when min >= max")
	}
}

func TestImageProxyDefaultsOn(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "http://x")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.ImageProxy {
		t.Fatal("ImageProxy should default on")
	}
	if !c.BlockPrivateNetworks {
		t.Fatal("BlockPrivateNetworks should default on")
	}
}

func TestImageProxyOff(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "http://x")
	t.Setenv("BFEED_IMAGE_PROXY", "off")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ImageProxy {
		t.Fatal("ImageProxy should be off")
	}
}

func TestParseAllowPrivateCIDRs(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "http://x")
	t.Setenv("BFEED_ALLOW_PRIVATE_CIDRS", "100.64.0.0/10, 192.168.0.0/16")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllowPrivateCIDRs) != 2 {
		t.Fatalf("got %d prefixes", len(c.AllowPrivateCIDRs))
	}
}

func TestInvalidCIDRErrors(t *testing.T) {
	t.Setenv("BFEED_BASE_URL", "http://x")
	t.Setenv("BFEED_ALLOW_PRIVATE_CIDRS", "not-a-cidr")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestSchedFactorRejectsNonFinite(t *testing.T) {
	for _, v := range []string{"NaN", "Inf", "+Inf", "0", "-2"} {
		t.Setenv("BFEED_BASE_URL", "http://x")
		t.Setenv("BFEED_SCHED_FACTOR", v)
		if _, err := Load(); err == nil {
			t.Fatalf("BFEED_SCHED_FACTOR=%q should be rejected", v)
		}
	}
}
