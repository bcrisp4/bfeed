package core

import "time"

type RescheduleConfig struct {
	Interval   time.Duration
	MaxBackoff time.Duration
}

// PollReschedule returns the next check time.
//   - errorCount == 0: now + Interval (honoring a larger Retry-After).
//   - errorCount > 0: exponential backoff Interval*2^errorCount capped at MaxBackoff, + jitter.
//   - retryAfter (if larger than the computed delay) wins, but is itself clamped
//     to MaxBackoff so a hostile server can't park a feed arbitrarily far out.
func PollReschedule(now time.Time, cfg RescheduleConfig, errorCount int, retryAfter time.Duration, jitter func(time.Duration) time.Duration) time.Time {
	delay := cfg.Interval
	if errorCount > 0 {
		shift := errorCount
		if shift > 16 {
			shift = 16 // avoid overflow before the cap clamps anyway
		}
		delay = cfg.Interval << uint(shift)
		if delay > cfg.MaxBackoff || delay <= 0 {
			delay = cfg.MaxBackoff
		}
		if jitter != nil {
			delay += jitter(delay)
		}
	}
	if retryAfter > delay {
		delay = retryAfter
		if delay > cfg.MaxBackoff {
			delay = cfg.MaxBackoff
		}
	}
	return now.Add(delay)
}
