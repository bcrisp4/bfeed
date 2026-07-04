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
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

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
	// ParseAndMutate, not FromDocument: FromDocument deep-clones the whole DOM
	// to keep the caller's doc untouched, but doc is discarded here anyway.
	parser := readability.NewParser()
	article, err := parser.ParseAndMutate(doc, u)
	if err != nil {
		return "", fmt.Errorf("readability: %w", err)
	}
	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		return "", fmt.Errorf("readability render: %w", err)
	}
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("readability: no main content extracted")
	}
	return out, nil
}

// decodeReader is the HTML-page counterpart of parse.decodeReader (B4) — same
// intent (decode to UTF-8 honoring declared encodings via WHATWG sniffing: BOM,
// then Content-Type charset, then <meta> prescan — never a statistical guess),
// different guards; don't try to unify them.
//
// The windows-1252 exception keeps UTF-8 whenever the sniff yields its
// uncertain windows-1252 answer — either the no-declaration legacy default
// (the sniff sees only the first 1KB, so an ASCII-heavy prefix would otherwise
// mojibake multibyte runes further down) or an explicit windows-1252/
// iso-8859-1 <meta> — but the page is valid UTF-8. Genuine cp1252 high bytes
// (0x80–0x9F punctuation) are bare UTF-8 continuation bytes, so utf8.Valid
// rejects real cp1252 text; only mislabeled UTF-8 is overridden, which is the
// correct outcome.
func decodeReader(page []byte, contentType string) io.Reader {
	enc, name, certain := charset.DetermineEncoding(page, contentType)
	r := io.Reader(bytes.NewReader(page))
	if certain || name != "windows-1252" || !utf8.Valid(page) {
		r = transform.NewReader(r, enc.NewDecoder())
	}
	// Parity with the old go-shiori dom.Parse path: recompose to NFC and strip
	// soft hyphens (U+00AD), so newly scraped text keeps matching pre-existing
	// rows and FTS queries (an embedded soft hyphen or decomposed accent would
	// otherwise break word matching). Chain built per call — transformers are
	// stateful and Extract runs on concurrent scrape workers.
	softHyphen := runes.Predicate(func(r rune) bool { return r == '\u00ad' })
	return transform.NewReader(r, transform.Chain(norm.NFD, runes.Remove(softHyphen), norm.NFC))
}
