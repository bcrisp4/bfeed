package core

import "time"

const (
	week            = 7 * 24 * time.Hour  // 604800s
	maxTTLInfluence = 30 * 24 * time.Hour // a publisher hint may slow a feed, but not silence it
)

// ScheduleConfig bounds the adaptive poll interval.
type ScheduleConfig struct {
	MinInterval time.Duration
	MaxInterval time.Duration
	Factor      float64
}

// AdaptiveInterval returns the next poll interval for a successfully-polled feed.
//
//   - weeklyCount: entries observed in the last week (COUNT; see Store.WeeklyEntryCount).
//   - feedTTL: publisher-declared minimum interval (0 if none); honoured but capped.
//   - jitter: small +/- spread to avoid a lockstep herd (nil disables it, for tests).
//
// Retry-After is deliberately absent: only 200/304 reach the success path, where
// it is always zero — it is honoured on the error path (PollReschedule).
func AdaptiveInterval(weeklyCount int, cfg ScheduleConfig, feedTTL time.Duration, jitter func(time.Duration) time.Duration) time.Duration {
	var iv time.Duration
	if weeklyCount <= 0 {
		iv = cfg.MaxInterval // quiet feed
	} else {
		iv = time.Duration(float64(week) / (float64(weeklyCount) * cfg.Factor))
	}
	iv = clamp(iv, cfg.MinInterval, cfg.MaxInterval)
	if feedTTL > 0 {
		iv = maxDur(iv, minDur(feedTTL, maxTTLInfluence))
	}
	if jitter != nil {
		iv += jitter(iv)
	}
	return iv
}

func clamp(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
