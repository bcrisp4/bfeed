package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestBuildMatch(t *testing.T) {
	cases := []struct{ in, want string }{
		{"rust async", `"rust" "async"*`},
		{"node.js", `"node.js"*`},
		{`foo "bar`, `"foo" """bar"*`},
		{"OR", `"OR"*`},
		{"c#", `"c#"*`},
		{"   ", ""},
		{"++", ""},
		{"a", `"a"*`},
		{"Rust  async  \"io\"", `"Rust" "async" """io"""*`},
		{"日本語", `"日本語"*`},
		{"🦀", ""},             // emoji-only: no indexable rune → dropped, no FTS5 syntax error
		{"🦀 rust", `"rust"*`}, // mixed: emoji token dropped, word survives
	}
	for _, c := range cases {
		if got := buildMatch(c.in); got != c.want {
			t.Errorf("buildMatch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSearchEmptyQueryNoRows(t *testing.T) {
	s := newTestStore(t)
	got, cur, err := s.Search(context.Background(), core.DefaultUserID, "   ", core.EntryFilter{})
	if err != nil || got != nil || cur != nil {
		t.Fatalf("empty query: got=%v cur=%v err=%v", got, cur, err)
	}
}

func TestSearchMatchesAndScopes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{
		ftsEntry(fid, "a", "Kubernetes networking", "pods", ""),
		ftsEntry(fid, "b", "Go modules", "deps", ""),
	}); err != nil {
		t.Fatal(err)
	}
	got, cur, err := s.Search(ctx, core.DefaultUserID, "kubernetes", core.EntryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GUID != "a" {
		t.Fatalf("got %d results, want 1 (guid a)", len(got))
	}
	if cur != nil {
		t.Fatalf("search must return a nil cursor (no pagination), got %+v", cur)
	}
	// AND semantics: both terms required.
	if got, _, _ := s.Search(ctx, core.DefaultUserID, "kubernetes modules", core.EntryFilter{}); len(got) != 0 {
		t.Fatalf("AND query matched %d, want 0", len(got))
	}
}

func TestSearchRanksByRelevance(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{
		ftsEntry(fid, "sparse", "alpha", "kubernetes", ""),
		ftsEntry(fid, "dense", "beta", "kubernetes kubernetes kubernetes kubernetes kubernetes", ""),
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Search(ctx, core.DefaultUserID, "kubernetes", core.EntryFilter{})
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d err=%v", len(got), err)
	}
	if got[0].GUID != "dense" {
		t.Fatalf("relevance order wrong: first=%s want dense", got[0].GUID)
	}
}

func TestSearchPlanNoTempBTree(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rows, err := s.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN
		 SELECT e.id FROM entries_fts JOIN entries e ON e.id = entries_fts.rowid
		 WHERE entries_fts MATCH 'kubernetes' AND e.user_id = 1 ORDER BY rank LIMIT 50`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("search sorts in memory:\n%s", plan)
	}
	if !strings.Contains(plan, "entries_fts") {
		t.Fatalf("search not driven by FTS5 virtual table:\n%s", plan)
	}
}
