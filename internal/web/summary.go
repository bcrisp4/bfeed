package web

import (
	"html"
	"regexp"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

// tagRE matches HTML tags so already-sanitised markup can be reduced to text.
var tagRE = regexp.MustCompile(`<[^>]*>`)

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

// summaryText derives a short, tag-free blurb for list rows from an entry's
// already-sanitised HTML. Prefers Summary (feed description) over full Content.
func summaryText(e *core.Entry) string {
	src := e.Summary
	if strings.TrimSpace(src) == "" {
		src = e.Content
	}
	if len(src) > maxSummaryScan {
		src = src[:maxSummaryScan]
	}
	return htmlToText(src)
}
