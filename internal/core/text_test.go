package core_test

import (
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestPlainText(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<a href="https://example.com">hi</a>`, "hi"},
		{`<p>Tom &amp; Jerry &mdash; friends</p>`, "Tom & Jerry — friends"},
		{"  multiple   spaces  <b>x</b>\n", "multiple spaces x"},
		{`<img src="https://example.com/a.png">`, ""},
		{"already plain", "already plain"},
		{"", ""},
	}
	for _, c := range cases {
		if got := core.PlainText(c.in); got != c.want {
			t.Errorf("PlainText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
