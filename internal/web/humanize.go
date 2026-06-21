package web

import (
	"fmt"
	"time"
)

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
