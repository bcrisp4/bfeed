package web

import (
	"testing"
	"time"
)

func TestHumanizeSince(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-2 * 24 * time.Hour), "2d ago"},
		{"old absolute", now.Add(-40 * 24 * time.Hour), now.Add(-40 * 24 * time.Hour).Format("2006-01-02")},
		{"future", now.Add(time.Hour), "just now"},
	}
	for _, c := range cases {
		if got := humanizeSince(c.t, now); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
