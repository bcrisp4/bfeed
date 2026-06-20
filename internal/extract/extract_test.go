package extract

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestExtractMainContent(t *testing.T) {
	page, err := os.ReadFile("testdata/article.html")
	if err != nil {
		t.Fatal(err)
	}
	html, err := New().Extract(context.Background(), "https://example.com/post", page)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(html, "first substantial paragraph") {
		t.Fatalf("main content missing:\n%s", html)
	}
	if strings.Contains(html, "copyright") {
		t.Fatalf("boilerplate not stripped:\n%s", html)
	}
}

func TestExtractEmptyIsError(t *testing.T) {
	if _, err := New().Extract(context.Background(), "https://example.com", []byte("<html></html>")); err == nil {
		t.Fatal("want error for contentless page")
	}
}
