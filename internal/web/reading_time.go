package web

import (
	"regexp"
	"strconv"
	"strings"
)

var tagRE = regexp.MustCompile(`<[^>]*>`)

// readingTime estimates reading duration from already-sanitised HTML at ~220 wpm.
// Returns "" when there are no words, else "N min read" (minimum "1 min read").
func readingTime(html string) string {
	text := tagRE.ReplaceAllString(html, " ")
	n := len(strings.Fields(text))
	if n == 0 {
		return ""
	}
	mins := (n + 219) / 220
	if mins < 1 {
		mins = 1
	}
	return strconv.Itoa(mins) + " min read"
}
