package sqlite

import (
	"context"
	"strings"
	"unicode"

	"github.com/bcrisp4/bfeed/internal/core"
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

// Search runs a bm25-ranked full-text query, scoped to userID, capped at 50.
// EntryFilter is reserved for future in-search filters; unused this iteration.
// Always returns a nil next-cursor — relevance order has no stable keyset, so
// this iteration does not paginate (see spec 2.1).
func (s *Store) Search(ctx context.Context, userID core.ID, query string, _ core.EntryFilter) ([]*core.Entry, *core.Cursor, error) {
	match := buildMatch(query)
	if match == "" {
		return nil, nil, nil
	}
	const q = `SELECT e.id, e.user_id, e.feed_id, e.guid, e.url, e.title, e.author, e.content, e.summary,
	                  e.published_at, e.status, e.starred, e.read_at, e.created_at, e.hash
	           FROM entries_fts JOIN entries e ON e.id = entries_fts.rowid
	           WHERE entries_fts MATCH ? AND e.user_id = ?
	           ORDER BY rank LIMIT 50`
	rows, err := s.db.QueryContext(ctx, q, match, int64(userID))
	if err != nil {
		return nil, nil, mapErr(err)
	}
	defer rows.Close()
	var out []*core.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, nil, mapErr(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapErr(err)
	}
	return out, nil, nil
}

func hasIndexable(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
