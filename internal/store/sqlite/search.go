package sqlite

import (
	"strings"
	"unicode"
)

// buildMatch turns raw user text into a safe FTS5 MATCH string. Every whitespace
// token is double-quoted (an embedded " is doubled), which makes all FTS5
// operators (* + ^ : - ( ) NEAR AND OR NOT) inert; tokens are implicitly ANDed;
// the final token gets a trailing * (outside its quotes) for prefix matching.
// Tokens with no indexable rune are dropped so a lone "++" can't become a
// zero-token quoted phrase (an FTS5 footgun). Returns "" when nothing remains.
func buildMatch(raw string) string {
	var quoted []string
	for _, tok := range strings.Fields(raw) {
		if !hasIndexable(tok) {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(tok, `"`, `""`)+`"`)
	}
	if len(quoted) == 0 {
		return ""
	}
	quoted[len(quoted)-1] += "*"
	return strings.Join(quoted, " ")
}

func hasIndexable(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
