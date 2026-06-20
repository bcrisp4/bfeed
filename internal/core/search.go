package core

import (
	"context"
	"log/slog"
	"strings"
)

// SearchService runs full-text queries over entries. It is FTS5-agnostic: it
// trims the query and forwards it with the user scope to the SearchIndex, which
// (in the sqlite adapter) builds the actual FTS5 MATCH. An empty query yields no
// results without touching the index.
type SearchService struct {
	idx SearchIndex
	log *slog.Logger
}

func NewSearchService(idx SearchIndex, log *slog.Logger) *SearchService {
	return &SearchService{idx: idx, log: log}
}

func (s *SearchService) Search(ctx context.Context, userID ID, query string, f EntryFilter) ([]*Entry, *Cursor, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, nil
	}
	return s.idx.Search(ctx, userID, query, f)
}
