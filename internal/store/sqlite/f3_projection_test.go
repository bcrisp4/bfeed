package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bcrisp4/bfeed/internal/core"
)

// F3: list and search projections truncate content/summary to a preview length so
// hot list pages don't read whole scraped articles. Truncation is character-based
// (SQLite substr on TEXT) so a multibyte rune is never split; GetEntry keeps full.
func TestListAndSearchTruncateContentPreview(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	p := time.Unix(1_700_000_100, 0).UTC()

	e := mkEntry(fid, "big", p)
	e.Content = "findme " + strings.Repeat("é", 3000) // 3007 runes, well over the 2048 preview
	e.Summary = strings.Repeat("ü", 3000)
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{e}); err != nil {
		t.Fatal(err)
	}

	list, _, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: n=%d err=%v", len(list), err)
	}
	if n := utf8.RuneCountInString(list[0].Content); n != 2048 {
		t.Fatalf("list content preview = %d runes, want 2048", n)
	}
	if !utf8.ValidString(list[0].Content) {
		t.Fatalf("list content preview is not valid UTF-8 (rune split)")
	}
	if n := utf8.RuneCountInString(list[0].Summary); n != 2048 {
		t.Fatalf("list summary preview = %d runes, want 2048", n)
	}

	found, _, err := s.Search(ctx, core.DefaultUserID, "findme", core.EntryFilter{})
	if err != nil || len(found) != 1 {
		t.Fatalf("search: n=%d err=%v", len(found), err)
	}
	if n := utf8.RuneCountInString(found[0].Content); n != 2048 {
		t.Fatalf("search content preview = %d runes, want 2048", n)
	}

	full, err := s.GetEntry(ctx, core.DefaultUserID, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(full.Content); n != 3007 {
		t.Fatalf("GetEntry content = %d runes, want full 3007", n)
	}
}
