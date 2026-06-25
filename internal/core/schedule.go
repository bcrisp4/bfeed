package core

import "time"

const (
	// Week is the adaptive-count window: WeeklyEntryCount samples entries over
	// [now-Week, now] and AdaptiveInterval divides Week by that count, so the
	// two must share one definition (the store/MemStore reference this).
	Week            = 7 * 24 * time.Hour  // 604800s
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
//   - jitter: small spread to avoid a lockstep herd (nil disables it, for tests).
//
// Jitter is applied to the adaptive interval BEFORE the publisher-TTL floor, so
// the floor — capped at maxTTLInfluence — is the true upper bound: a malformed
// "yearly" hint can never push the interval past the cap. Retry-After is
// deliberately absent: only 200/304 reach the success path, where it is always
// zero — it is honoured on the error path (PollReschedule).
func AdaptiveInterval(weeklyCount int, cfg ScheduleConfig, feedTTL time.Duration, jitter func(time.Duration) time.Duration) time.Duration {
	var iv time.Duration
	if weeklyCount <= 0 {
		iv = cfg.MaxInterval // quiet feed
	} else {
		iv = time.Duration(float64(Week) / (float64(weeklyCount) * cfg.Factor))
	}
	iv = min(max(iv, cfg.MinInterval), cfg.MaxInterval)
	if jitter != nil {
		iv += jitter(iv)
	}
	if feedTTL > 0 {
		iv = max(iv, min(feedTTL, maxTTLInfluence))
	}
	return iv
}
