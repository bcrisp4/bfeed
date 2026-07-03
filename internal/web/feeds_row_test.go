package web

import (
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// The feedrow/feedgrouphead templates must omit the unread/total counts when
// ShowCounts is false (stats lookup failed) rather than render fabricated zeros.
func TestFeedRowHidesCountsWhenStatsUnavailable(t *testing.T) {
	tmpl := parseTemplates()

	render := func(name string, data any) string {
		var b strings.Builder
		if err := tmpl["feedrow"].ExecuteTemplate(&b, name, data); err != nil {
			t.Fatalf("execute %s: %v", name, err)
		}
		return b.String()
	}

	shown := render("feedrow", feedRowVM{ID: 1, Title: "Blog", Host: "b.test", Unread: 3, Total: 9, ShowCounts: true})
	if !strings.Contains(shown, "3 unread") {
		t.Errorf("ShowCounts=true should render counts, got %s", shown)
	}
	hidden := render("feedrow", feedRowVM{ID: 1, Title: "Blog", Host: "b.test", Unread: 3, Total: 9, ShowCounts: false})
	if strings.Contains(hidden, "unread</span> / ") || strings.Contains(hidden, "3 unread") {
		t.Errorf("ShowCounts=false should omit per-feed counts, got %s", hidden)
	}

	headHidden := render("feedgrouphead", feedGroupHeadVM{Title: "Tech", FeedCount: 2, Unread: 5, ShowCounts: false})
	if strings.Contains(headHidden, "unread") {
		t.Errorf("group head should omit unread count when ShowCounts=false, got %s", headHidden)
	}
	if !strings.Contains(headHidden, "2 feeds") {
		t.Errorf("group head should still show feed count, got %s", headHidden)
	}
}

func TestFeedHost(t *testing.T) {
	cases := []struct {
		rawURL string
		want   string
	}{
		{"https://example.com/feed.xml", "example.com"},
		{"https://blog.example.org/rss", "blog.example.org"},
		{"https://example.com:8080/feed", "example.com:8080"},
		{"not a url at all", "not a url at all"},
		{"", ""},
	}
	for _, c := range cases {
		if got := feedHost(c.rawURL); got != c.want {
			t.Errorf("feedHost(%q)=%q want %q", c.rawURL, got, c.want)
		}
	}
}

func TestBuildFeedRow(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	checkedAt := now.Add(-2 * time.Hour)
	nextCheck := now.Add(1 * time.Hour)
	catID := core.ID(7)

	t.Run("never checked (pending)", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 1, FeedURL: "https://example.com/feed.xml",
			Title: "Example", NextCheckAt: nextCheck,
			ErrorCount: 0,
		}
		// mark as in-flight
		h.busy.start(f.ID)
		row := h.buildFeedRow(f, core.FeedEntryStats{Unread: 3, Total: 10}, now)
		if row.Title != "Example" {
			t.Errorf("Title=%q want %q", row.Title, "Example")
		}
		if row.Host != "example.com" {
			t.Errorf("Host=%q want %q", row.Host, "example.com")
		}
		if row.Unread != 3 || row.Total != 10 {
			t.Errorf("counts Unread=%d Total=%d", row.Unread, row.Total)
		}
		if row.Updated != "" {
			t.Errorf("Updated=%q want empty (never checked)", row.Updated)
		}
		if !row.Pending {
			t.Error("Pending should be true for in-flight feed with no CheckedAt")
		}
		if row.Refreshing {
			t.Error("Refreshing should be false when no CheckedAt")
		}
	})

	t.Run("checked and idle", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 2, FeedURL: "https://example.com/feed.xml",
			Title: "Example", CheckedAt: &checkedAt, NextCheckAt: nextCheck,
			ErrorCount: 0,
		}
		row := h.buildFeedRow(f, core.FeedEntryStats{Unread: 1, Total: 5}, now)
		if row.Updated != "2h ago" {
			t.Errorf("Updated=%q want %q", row.Updated, "2h ago")
		}
		if row.Next != "in 1h" {
			t.Errorf("Next=%q want %q", row.Next, "in 1h")
		}
		if row.Pending || row.Refreshing {
			t.Error("Pending and Refreshing should be false when idle")
		}
		if row.Stalled {
			t.Error("Stalled should be false with ErrorCount=0")
		}
	})

	t.Run("checked and refreshing", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 3, FeedURL: "https://example.com/feed.xml",
			Title: "Example", CheckedAt: &checkedAt, NextCheckAt: nextCheck,
		}
		h.busy.start(f.ID)
		row := h.buildFeedRow(f, core.FeedEntryStats{}, now)
		if !row.Refreshing {
			t.Error("Refreshing should be true for in-flight feed with CheckedAt")
		}
		if row.Pending {
			t.Error("Pending should be false when CheckedAt is set")
		}
	})

	t.Run("stalled", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 4, FeedURL: "https://example.com/feed.xml",
			Title: "Example", CheckedAt: &checkedAt, NextCheckAt: nextCheck,
			ErrorCount: 5,
		}
		row := h.buildFeedRow(f, core.FeedEntryStats{}, now)
		if !row.Stalled {
			t.Error("Stalled should be true when ErrorCount >= errorLimit")
		}
	})

	t.Run("user title override", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 5, FeedURL: "https://example.com/feed.xml",
			Title: "Poll Title", UserTitle: "My Custom Name",
			NextCheckAt: nextCheck,
		}
		row := h.buildFeedRow(f, core.FeedEntryStats{}, now)
		if row.Title != "My Custom Name" {
			t.Errorf("Title=%q want %q", row.Title, "My Custom Name")
		}
	})

	t.Run("category id propagated", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 6, FeedURL: "https://example.com/feed.xml",
			Title: "Example", CategoryID: &catID,
			NextCheckAt: nextCheck,
		}
		row := h.buildFeedRow(f, core.FeedEntryStats{}, now)
		if row.CategoryID != int64(catID) {
			t.Errorf("CategoryID=%d want %d", row.CategoryID, catID)
		}
	})

	t.Run("next empty when past", func(t *testing.T) {
		h := &Handler{errorLimit: 5, busy: newInflightSet()}
		f := &core.Feed{
			ID: 7, FeedURL: "https://example.com/feed.xml",
			Title: "Example", NextCheckAt: now.Add(-time.Minute),
		}
		row := h.buildFeedRow(f, core.FeedEntryStats{}, now)
		if row.Next != "" {
			t.Errorf("Next=%q want empty when NextCheckAt is in the past", row.Next)
		}
	})
}
