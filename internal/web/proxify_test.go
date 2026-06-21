package web

import (
	"strings"
	"testing"
)

func TestProxifyImagesNoWrapperAndSkipsData(t *testing.T) {
	in := `<p>hi <img src="https://o/x.png"> <img src="data:image/png;base64,AAAA"></p>`
	out := proxifyImages(in, func(u string) string { return "P(" + u + ")" })
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
