package core

import (
	"testing"
	"time"
)

var noJitter = func(d time.Duration) time.Duration { return 0 }

func TestRescheduleHappyPath(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour}
	got := PollReschedule(now, cfg, 0, 0, noJitter)
	if !got.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("happy reschedule = %v", got)
	}
}

func TestRescheduleBackoffDoubles(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour}
	d2 := PollReschedule(now, cfg, 2, 0, noJitter).Sub(now) // 15m * 2^2 = 60m
	if d2 != 60*time.Minute {
		t.Fatalf("backoff(2) = %v, want 60m", d2)
	}
}

func TestRescheduleBackoffCapped(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour}
	d := PollReschedule(now, cfg, 30, 0, noJitter).Sub(now)
	if d != 24*time.Hour {
		t.Fatalf("capped backoff = %v, want 24h", d)
	}
}

func TestRescheduleHonorsRetryAfter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour}
	d := PollReschedule(now, cfg, 0, 90*time.Minute, noJitter).Sub(now)
	if d != 90*time.Minute {
		t.Fatalf("retry-after not honored: %v", d)
	}
}

func TestRescheduleRetryAfterBeatsBackoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := RescheduleConfig{Interval: 15 * time.Minute, MaxBackoff: 24 * time.Hour}
	// errorCount=1 → backoff 30m; Retry-After 2h must win.
	d := PollReschedule(now, cfg, 1, 2*time.Hour, noJitter).Sub(now)
	if d != 2*time.Hour {
		t.Fatalf("retry-after should beat backoff: got %v", d)
	}
}
