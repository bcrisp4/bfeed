package web

import (
	"html"
	"regexp"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

var (
	tagRE    = regexp.MustCompile(`<[^>]*>`)
	anchorRE = regexp.MustCompile(`(?is)<a\b[^>]*>.*?</a>`)
	urlRE    = regexp.MustCompile(`^(https?://|www\.)`)
)

// maxSummaryScan bounds how much HTML summaryText inspects. A list blurb is
// CSS-clamped to ~2 lines, so scanning only a prefix avoids a full regex pass
// over a large full-content article on every row of every list render.
const maxSummaryScan = 2048

// htmlToText converts already-sanitised HTML to plain text: strip tags, decode
// entities, collapse whitespace. Decoding matters because the template
// re-escapes the result — leaving entities encoded would double-escape them
// (e.g. "AT&amp;T" would otherwise display to the user as the literal "AT&amp;T").
func htmlToText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// linkOnly reports whether HTML carries no text beyond its links — e.g. a
// Hacker News item whose whole summary is a bare "<a>Comments</a>". Such a
// blurb is noise as a list preview, so summaryText skips it.
func linkOnly(htmlSrc string) bool {
	return strings.TrimSpace(htmlToText(anchorRE.ReplaceAllString(htmlSrc, " "))) == ""
}

// summaryText derives a short, tag-free blurb for list rows. It prefers the feed
// Summary, falls back to full Content, and skips sources with no real preview
// text: empty, nothing but links (HN-style), or a single bare URL.
func summaryText(e *core.Entry) string {
	for _, src := range [2]string{e.Summary, e.Content} {
		if strings.TrimSpace(src) == "" {
			continue
		}
		scan := src
		if len(scan) > maxSummaryScan {
			scan = scan[:maxSummaryScan]
		}
		if linkOnly(scan) {
			continue
		}
		text := htmlToText(scan)
		if text == "" || (urlRE.MatchString(text) && !strings.ContainsAny(text, " ")) {
			continue
		}
		return text
	}
	return ""
}
