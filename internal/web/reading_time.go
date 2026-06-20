package web

import (
	"strconv"
	"strings"
)

// readingTime estimates reading duration from already-sanitised HTML at ~220 wpm.
// Returns "" when there are no words, else "N min read" (minimum "1 min read",
// since the ceiling division of any positive word count rounds up to 1).
func readingTime(s string) string {
	n := len(strings.Fields(htmlToText(s)))
	if n == 0 {
		return ""
	}
	mins := (n + 219) / 220
	return strconv.Itoa(mins) + " min read"
}
