package web

import (
	"fmt"
	"time"
)

// humanizeSince renders the gap between t and now as a short relative string,
// falling back to an absolute date for anything older than ~30 days. Future
// timestamps render as "just now".
func humanizeSince(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
