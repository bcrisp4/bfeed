package web

import (
	"html"
	"regexp"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

var (
	tagRE = regexp.MustCompile(`<[^>]*>`)
	urlRE = regexp.MustCompile(`(?:https?://|www\.)\S+`)
)

const (
	// maxSummaryScan bounds how much HTML is inspected per source. A list blurb
	// is CSS-clamped to ~2 lines, so a prefix is plenty and avoids a full regex
	// pass over a large scraped article on every row of every list render.
	maxSummaryScan = 2048
	// A preview is rejected when URLs make up more than maxLinkDensity of its
	// text, or it carries fewer than minPreviewWords of prose. Calibrated against
	// real feeds: link/metadata-dump summaries (e.g. hnrss "Article URL: … Comments
	// URL: …") score 0.6–0.8 density; genuine prose scores 0.0–0.05.
	maxLinkDensity  = 0.4
	minPreviewWords = 5
)

// htmlToText converts already-sanitised HTML to plain text: strip tags, decode
// entities, collapse whitespace. Decoding matters because the template
// re-escapes the result — leaving entities encoded would double-escape them
// (e.g. "AT&amp;T" would otherwise display to the user as the literal "AT&amp;T").
func htmlToText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// goodPreview reports whether plain text reads as real prose worth showing as a
// list blurb — not a link/URL dump and not a bare "read more" stub. It judges
// the text itself, not the source feed, so it generalises across sites.
func goodPreview(text string) bool {
	if text == "" {
		return false
	}
	urlChars := 0
	for _, u := range urlRE.FindAllString(text, -1) {
		urlChars += len(u)
	}
	if float64(urlChars)/float64(len(text)) > maxLinkDensity {
		return false
	}
	return len(strings.Fields(urlRE.ReplaceAllString(text, " "))) >= minPreviewWords
}

// summaryText derives a short list-row blurb, preferring the feed's hand-written
// Summary and falling back to the (often scraped, full-text) Content. Sources
// that are only links/metadata are skipped, so a feed whose summary is a bare
// link (e.g. Hacker News) shows the article's opening instead — and nothing only
// when neither source carries real prose.
func summaryText(e *core.Entry) string {
	for _, src := range [2]string{e.Summary, e.Content} {
		if strings.TrimSpace(src) == "" {
			continue
		}
		scan := src
		if len(scan) > maxSummaryScan {
			scan = scan[:maxSummaryScan]
		}
		if text := htmlToText(scan); goodPreview(text) {
			return text
		}
	}
	return ""
}
