package web

import (
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestSummaryText(t *testing.T) {
	cases := []struct {
		name string
		e    *core.Entry
		want string
	}{
		{"prefers summary", &core.Entry{Summary: "<p>hi  there</p>", Content: "<p>body</p>"}, "hi there"},
		{"falls back to content", &core.Entry{Summary: "", Content: "<p>just  body</p>"}, "just body"},
		{"empty", &core.Entry{}, ""},
		// Entities must be decoded so the template's re-escaping renders them once,
		// not double-escaped (e.g. a literal "AT&amp;T") in the list row.
		{"decodes entities", &core.Entry{Summary: "<p>AT&amp;T &amp; co &mdash; news</p>"}, "AT&T & co — news"},
		// HN-style: a summary that is only a link is noise; fall through to Content.
		{"link-only summary skipped", &core.Entry{Summary: `<a href="https://news.ycombinator.com/item?id=1">Comments</a>`, Content: "<p>real body</p>"}, "real body"},
		{"link-only with no content yields empty", &core.Entry{Summary: `<a href="https://x/1">Comments</a>`}, ""},
		{"bare url summary skipped", &core.Entry{Summary: "https://example.com/article", Content: "<p>body</p>"}, "body"},
		// A link with surrounding context is real preview text — keep it.
		{"link with context kept", &core.Entry{Summary: `<p>Read <a href="x">the post</a> now</p>`}, "Read the post now"},
	}
	for _, c := range cases {
		if got := summaryText(c.e); got != c.want {
			t.Errorf("%s: summaryText() = %q, want %q", c.name, got, c.want)
		}
	}
}
