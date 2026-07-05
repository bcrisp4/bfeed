package web

import (
	"strings"
	"testing"
)

func TestTransformContentNoWrapperAndSkipsData(t *testing.T) {
	in := `<p>hi <img src="https://o/x.png"> <img src="data:image/png;base64,AAAA"></p>`
	out := transformContent(in, func(u string) string { return "P(" + u + ")" })
	if strings.Contains(out, "<html") || strings.Contains(out, "<body") {
		t.Fatalf("document wrapper injected: %s", out)
	}
	if !strings.Contains(out, "P(https://o/x.png)") {
		t.Fatalf("http img not rewritten: %s", out)
	}
	if !strings.Contains(out, "data:image/png;base64,AAAA") {
		t.Fatalf("data uri must be left untouched: %s", out)
	}
}

func TestTransformContentPromotion(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		promote bool
	}{
		{"bare img", `<p><img src="https://o/x.png"></p>`, true},
		{"whitespace text", "<p>\n  <img src=\"https://o/x.png\">  \n</p>", true},
		{"nbsp text", `<p>&nbsp;<img src="https://o/x.png">&nbsp;</p>`, true},
		{"linked img", `<p><a href="https://o/full"><img src="https://o/x.png"></a></p>`, true},
		{"linked img with whitespace", "<p> <a href=\"https://o/full\">\n<img src=\"https://o/x.png\">\n</a> </p>", true},
		{"nested in div", `<div><p><img src="https://o/x.png"></p></div>`, true},
		{"real text", `<p>hello <img src="https://o/x.png"></p>`, false},
		{"img plus br", `<p><img src="https://o/x.png"><br></p>`, false},
		{"two imgs", `<p><img src="https://o/a.png"><img src="https://o/b.png"></p>`, false},
		{"link with text", `<p><a href="https://o/">go <img src="https://o/x.png"></a></p>`, false},
		{"link with two imgs", `<p><a href="https://o/"><img src="https://o/a.png"><img src="https://o/b.png"></a></p>`, false},
		{"link only no img", `<p><a href="https://o/">go</a></p>`, false},
		{"no img", `<p>plain</p>`, false},
		{"empty p", `<p></p>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := transformContent(tc.in, nil) // nil rewrite: promotion must not depend on the proxy
			hasFigure := strings.Contains(out, "<figure")
			if hasFigure != tc.promote {
				t.Fatalf("promoted=%v want %v: %s", hasFigure, tc.promote, out)
			}
			if tc.promote && strings.Contains(out, "<p") {
				t.Fatalf("promoted output still contains a <p>: %s", out)
			}
			if !tc.promote && !strings.Contains(out, "<p") {
				t.Fatalf("non-promoted output lost its <p>: %s", out)
			}
		})
	}
}

func TestTransformContentKeepsAttrsAndProxifies(t *testing.T) {
	in := `<p class="c"><img src="https://o/x.png" alt="a"></p>`
	out := transformContent(in, func(u string) string { return "P(" + u + ")" })
	if !strings.Contains(out, `<figure class="c">`) {
		t.Fatalf("p attrs not carried onto figure: %s", out)
	}
	if !strings.Contains(out, "P(https://o/x.png)") {
		t.Fatalf("img inside promoted figure not proxified: %s", out)
	}
	if !strings.Contains(out, `alt="a"`) {
		t.Fatalf("img attrs lost: %s", out)
	}
}
