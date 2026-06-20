package web

import (
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

// summaryText derives a short, tag-free blurb for list rows from an entry's
// already-sanitised HTML. Prefers Summary (feed description) over full Content.
func summaryText(e *core.Entry) string {
	src := e.Summary
	if strings.TrimSpace(src) == "" {
		src = e.Content
	}
	text := tagRE.ReplaceAllString(src, " ")
	return strings.Join(strings.Fields(text), " ")
}
