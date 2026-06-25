package web

import (
	"fmt"
	"time"
)

// humanizeUntil renders the gap from now to a future t as "in 5m" / "in 3h",
// an absolute date ("on 2 May 2026") beyond 24h, and "" for a past-or-now t
// (so the caller can omit a stale "next" time).
func humanizeUntil(t, now time.Time) string {
	d := t.Sub(now)
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return "in <1m"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return "on " + t.Format("2 Jan 2006")
	}
}

// humanizeSince renders the gap between t and now as a short relative string
// for the last 24 hours ("just now", "5m ago", "3h ago"), and an absolute date
// ("2 May 2026") for anything older. Future timestamps render as "just now".
func humanizeSince(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("2 Jan 2006")
	}
}
