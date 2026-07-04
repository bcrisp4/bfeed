package core

import (
	"html"
	"regexp"
	"strings"
)

var plainTextTagRE = regexp.MustCompile(`<[^>]*>`)

// PlainText reduces (already-sanitised) HTML to visible text: strip tags, decode
// entities, collapse whitespace. Used to feed the full-text index a text-only
// projection so searches match visible words, not markup (tag names, attributes,
// URLs). Mirrors web.htmlToText — the two must agree on what "visible text" means.
func PlainText(s string) string {
	s = plainTextTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
