package sanitize

import (
	"strings"
	"testing"
)

func TestSanitizeStripsActiveContent(t *testing.T) {
	in := `<p onclick="x()">hi<script>evil()</script><iframe src="y"></iframe></p>`
	out := New().Sanitize(in, "https://b.test/")
	for _, bad := range []string{"<script", "onclick", "<iframe"} {
		if strings.Contains(out, bad) {
			t.Fatalf("sanitised output still contains %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("dropped safe text: %s", out)
	}
}

func TestSanitizeStripsTrackingParams(t *testing.T) {
	in := `<a href="https://e.test/p?utm_source=x&id=7&fbclid=z">go</a>`
	out := New().Sanitize(in, "https://b.test/")
	if strings.Contains(out, "utm_source") || strings.Contains(out, "fbclid") {
		t.Fatalf("tracking params not stripped: %s", out)
	}
	if !strings.Contains(out, "id=7") {
		t.Fatalf("legit param dropped: %s", out)
	}
}

func TestSanitizeDropsTrackingPixel(t *testing.T) {
	in := `<img src="https://t.test/p.gif" width="1" height="1"><img src="https://i.test/real.jpg">`
	out := New().Sanitize(in, "https://b.test/")
	if strings.Contains(out, "p.gif") {
		t.Fatalf("1x1 pixel not dropped: %s", out)
	}
	if !strings.Contains(out, "real.jpg") {
		t.Fatalf("real image dropped: %s", out)
	}
}
