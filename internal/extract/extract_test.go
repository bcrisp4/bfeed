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
	html, err := New().Extract(context.Background(), "https://example.com/post", page, "text/html; charset=utf-8")
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
	if _, err := New().Extract(context.Background(), "https://example.com", []byte("<html></html>"), "text/html"); err == nil {
		t.Fatal("want error for contentless page")
	}
}

// utf8Page loads testdata/utf8-sparse.html: a short, mostly-ASCII English
// article with two curly apostrophes (U+2019) — the shape statistical charset
// detectors misguess as windows-1252 (issue #96). metaCharset=false strips the
// <meta charset="utf-8"> line to model a page with no declaration at all.
func utf8Page(t *testing.T, metaCharset bool) []byte {
	t.Helper()
	page, err := os.ReadFile("testdata/utf8-sparse.html")
	if err != nil {
		t.Fatal(err)
	}
	if !metaCharset {
		page = []byte(strings.Replace(string(page), "  <meta charset=\"utf-8\">\n", "", 1))
	}
	return page
}

// Regression for issue #96: a valid UTF-8 page served with a bare
// "text/html" Content-Type (no charset param) must not be mojibaked by a
// statistical charset guess — the in-band <meta charset="utf-8"> wins.
func TestExtractKeepsUTF8WhenHeaderOmitsCharset(t *testing.T) {
	html, err := New().Extract(context.Background(), "https://example.com/on-intent/", utf8Page(t, true), "text/html")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.Contains(html, "â€") { // "â€" — the UTF-8-as-cp1252 mojibake signature
		t.Fatalf("output mojibaked:\n%s", html)
	}
	if !strings.Contains(html, "It’s") {
		t.Fatalf("curly apostrophe lost:\n%s", html)
	}
}

// A page with no charset anywhere (no header param, no meta, no BOM) whose
// bytes are valid UTF-8 must stay UTF-8 rather than fall back to windows-1252.
// This is the test that exercises decodeReader's utf8.Valid guard: with the
// meta stripped, the ASCII-only first 1KB makes the WHATWG sniff bottom out at
// windows-1252 (the fixture's first multibyte rune sits past byte 1024).
func TestExtractUndeclaredValidUTF8StaysUTF8(t *testing.T) {
	html, err := New().Extract(context.Background(), "https://example.com/post", utf8Page(t, false), "text/html")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.Contains(html, "â€") { // UTF-8-as-cp1252 mojibake signature
		t.Fatalf("output mojibaked:\n%s", html)
	}
	if !strings.Contains(html, "It’s") {
		t.Fatalf("curly apostrophe lost:\n%s", html)
	}
}

// The old extraction path (go-shiori dom.Parse) always NFC-normalized and
// stripped soft hyphens (U+00AD); decodeReader must keep doing both so newly
// scraped text keeps matching older rows and FTS word queries.
func TestExtractStripsSoftHyphensAndNormalizesNFC(t *testing.T) {
	page := "<!DOCTYPE html><html><head><title>Test</title></head><body><article><h1>Post</h1>\n" +
		strings.Repeat("<p>A long enough paragraph of plain English filler so readability keeps the "+
			"article body, mentioning Zusam\u00admenarbeit and a decomposed re\u0301sume\u0301 in the middle of it.</p>\n", 8) +
		"</article></body></html>"
	html, err := New().Extract(context.Background(), "https://example.com/post", []byte(page), "text/html; charset=utf-8")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.ContainsRune(html, '\u00ad') {
		t.Fatalf("soft hyphen survived:\n%s", html)
	}
	if !strings.Contains(html, "Zusammenarbeit") {
		t.Fatalf("soft-hyphenated word not joined:\n%s", html)
	}
	if !strings.Contains(html, "résumé") { // NFC form
		t.Fatalf("NFD text not recomposed to NFC:\n%s", html)
	}
}

// A legacy-charset page declared via the HTTP Content-Type must be decoded to
// UTF-8. Body bytes are windows-1251 ("\xd2\xe5\xf1\xf2" = "Тест").
func TestExtractDecodesHeaderDeclaredCharset(t *testing.T) {
	page := "<!DOCTYPE html><html><head><title>Test</title></head><body><article><h1>Post</h1>\n" +
		strings.Repeat("<p>A long enough paragraph of plain English filler so readability keeps the "+
			"article body around the word \xd2\xe5\xf1\xf2 in the middle of it.</p>\n", 8) +
		"</article></body></html>"
	html, err := New().Extract(context.Background(), "https://example.com/post", []byte(page), "text/html; charset=windows-1251")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(html, "Тест") {
		t.Fatalf("windows-1251 body not decoded:\n%s", html)
	}
}
