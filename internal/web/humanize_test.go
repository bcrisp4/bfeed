package web

import (
	"testing"
	"time"
)

func TestHumanizeUntil(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-time.Minute), ""},          // past → empty
		{now, ""},                            // now → empty
		{now.Add(30 * time.Second), "in <1m"},
		{now.Add(5 * time.Minute), "in 5m"},
		{now.Add(3 * time.Hour), "in 3h"},
		{now.Add(48 * time.Hour), "on " + now.Add(48*time.Hour).Format("2 Jan 2006")},
	}
	for _, c := range cases {
		if got := humanizeUntil(c.t, now); got != c.want {
			t.Errorf("humanizeUntil(%v)=%q want %q", c.t.Sub(now), got, c.want)
		}
	}
}

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
		{"just under a day", now.Add(-23 * time.Hour), "23h ago"},
		{"exactly a day is absolute", now.Add(-24 * time.Hour), now.Add(-24 * time.Hour).Format("2 Jan 2006")},
		{"two days is absolute", now.Add(-2 * 24 * time.Hour), now.Add(-2 * 24 * time.Hour).Format("2 Jan 2006")},
		{"old absolute", now.Add(-400 * 24 * time.Hour), now.Add(-400 * 24 * time.Hour).Format("2 Jan 2006")},
		{"future", now.Add(time.Hour), "just now"},
	}
	for _, c := range cases {
		if got := humanizeSince(c.t, now); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
