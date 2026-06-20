// Package extract adapts readeck/go-readability to core.Extractor: it pulls
// main-article HTML from a fetched page. Output is RAW and MUST be sanitised
// before persistence (the scrape service does this).
package extract

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"

	"github.com/bcrisp4/bfeed/internal/core"
)

// Extractor pulls main-article HTML from a fetched page using Mozilla Readability.
type Extractor struct{}

// New returns a ready-to-use Extractor.
func New() *Extractor { return &Extractor{} }

var _ core.Extractor = (*Extractor)(nil)

// Extract parses page HTML at pageURL and returns the main article HTML.
// The returned HTML is raw and must be sanitised before persistence.
func (e *Extractor) Extract(_ context.Context, pageURL string, page []byte) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return "", fmt.Errorf("bad page url: %w", err)
	}
	article, err := readability.FromReader(bytes.NewReader(page), u)
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
