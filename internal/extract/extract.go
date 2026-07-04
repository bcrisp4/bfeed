// Package extract adapts readeck/go-readability to core.Extractor: it pulls
// main-article HTML from a fetched page. Output is RAW and MUST be sanitised
// before persistence (the scrape service does this).
package extract

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"

	"github.com/bcrisp4/bfeed/internal/core"
)

// Extractor pulls main-article HTML from a fetched page using Mozilla Readability.
type Extractor struct{}

// New returns a ready-to-use Extractor.
func New() *Extractor { return &Extractor{} }

var _ core.Extractor = (*Extractor)(nil)

// Extract parses page HTML at pageURL and returns the main article HTML.
// The returned HTML is raw and must be sanitised before persistence.
//
// The page is decoded to UTF-8 here (honoring HTTP Content-Type, BOM and
// <meta charset>) and parsed with html.Parse before being handed to
// readability.FromDocument. Do NOT "simplify" this back to
// readability.FromReader: that routes through go-shiori dom.Parse, which
// statistically guesses the charset and mojibakes valid UTF-8 pages (#96).
func (e *Extractor) Extract(_ context.Context, pageURL string, page []byte, contentType string) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return "", fmt.Errorf("bad page url: %w", err)
	}
	doc, err := html.Parse(decodeReader(page, contentType))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	article, err := readability.FromDocument(doc, u)
	if err != nil {
		return "", fmt.Errorf("readability: %w", err)
	}
	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		return "", fmt.Errorf("readability render: %w", err)
	}
	html := buf.String()
	if strings.TrimSpace(html) == "" {
		return "", fmt.Errorf("readability: no main content extracted")
	}
	return html, nil
}

// decodeReader mirrors parse.decodeReader (B4): decode to UTF-8 honoring
// declared encodings (BOM, then Content-Type charset, then <meta> prescan)
// via WHATWG sniffing — never a statistical guess. When the sniff bottoms out
// at its windows-1252 legacy default but the whole page is valid UTF-8, keep
// UTF-8: the sniff only sees the first 1KB, so an ASCII-heavy prefix would
// otherwise mojibake multibyte runes further down.
func decodeReader(page []byte, contentType string) io.Reader {
	enc, name, certain := charset.DetermineEncoding(page, contentType)
	if !certain && name == "windows-1252" && utf8.Valid(page) {
		return bytes.NewReader(page)
	}
	return transform.NewReader(bytes.NewReader(page), enc.NewDecoder())
}
