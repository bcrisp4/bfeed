package core

import (
	"testing"
	"time"
)

func cfg() ScheduleConfig {
	return ScheduleConfig{MinInterval: 5 * time.Minute, MaxInterval: 24 * time.Hour, Factor: 1}
}

func TestAdaptiveInterval(t *testing.T) {
	tests := []struct {
		name        string
		weeklyCount int
		feedTTL     time.Duration
		want        time.Duration
	}{
		{"quiet feed -> max", 0, 0, 24 * time.Hour},
		{"negative count -> max", -3, 0, 24 * time.Hour},
		{"14 per week -> 12h (in range)", 14, 0, Week / 14},
		{"low count clamps to max", 2, 0, 24 * time.Hour},
		{"very busy -> clamped to min", 100000, 0, 5 * time.Minute},
		{"ttl raises a min-clamped interval", 100000, 5 * time.Hour, 5 * time.Hour},
		{"ttl below computed is ignored", 14, time.Minute, Week / 14},
		{"ttl capped at 30d", 0, 400 * 24 * time.Hour, 30 * 24 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AdaptiveInterval(tc.weeklyCount, cfg(), tc.feedTTL, nil)
			if got != tc.want {
				t.Fatalf("AdaptiveInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdaptiveIntervalJitterApplied(t *testing.T) {
	jit := func(d time.Duration) time.Duration { return d / 10 }
	got := AdaptiveInterval(14, cfg(), 0, jit)
	want := Week/14 + (Week/14)/10
	if got != want {
		t.Fatalf("jittered = %v, want %v", got, want)
	}
}

func TestAdaptiveIntervalJitterDoesNotBreachTTLCap(t *testing.T) {
	jit := func(d time.Duration) time.Duration { return d / 4 } // max positive jitter
	got := AdaptiveInterval(0, cfg(), 400*24*time.Hour, jit)
	if got != 30*24*time.Hour {
		t.Fatalf("got %v, want 30d — jitter applied after the TTL cap breaches the ceiling", got)
	}
}
