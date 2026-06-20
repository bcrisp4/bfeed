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
	}
	for _, c := range cases {
		if got := summaryText(c.e); got != c.want {
			t.Errorf("%s: summaryText() = %q, want %q", c.name, got, c.want)
		}
	}
}
