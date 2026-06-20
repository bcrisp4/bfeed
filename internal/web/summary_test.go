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
	}
	for _, c := range cases {
		if got := summaryText(c.e); got != c.want {
			t.Errorf("%s: summaryText() = %q, want %q", c.name, got, c.want)
		}
	}
}
