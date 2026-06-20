package sqlite

import "testing"

func TestBuildMatch(t *testing.T) {
	cases := []struct{ in, want string }{
		{"rust async", `"rust" "async"*`},
		{"node.js", `"node.js"*`},
		{`foo "bar`, `"foo" "bar"*`},
		{"OR", `"OR"*`},
		{"c#", `"c#"*`},
		{"   ", ""},
		{"++", ""},
		{"a", `"a"*`},
		{"Rust  async  \"io\"", `"Rust" "async" "io"*`},
		{"日本語", `"日本語"*`},
	}
	for _, c := range cases {
		if got := buildMatch(c.in); got != c.want {
			t.Errorf("buildMatch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
