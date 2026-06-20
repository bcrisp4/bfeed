package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/bcrisp4/bfeed/internal/core"
)

// ftsIDs returns the entries.id rowids matching an FTS5 query, rank-ordered.
func ftsIDs(t *testing.T, s *Store, match string) []int64 {
	t.Helper()
	rows, err := s.db.Query(`SELECT rowid FROM entries_fts WHERE entries_fts MATCH ? ORDER BY rank`, match)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func ftsEntry(feedID core.ID, guid, title, content, summary string) *core.Entry {
	p := time.Unix(1_700_000_100, 0).UTC()
	return &core.Entry{
		UserID: core.DefaultUserID, FeedID: feedID, GUID: guid, URL: "https://f.test/" + guid,
		Title: title, Content: content, Summary: summary, PublishedAt: p, Status: core.StatusUnread,
		CreatedAt: p, Hash: "h-" + guid,
	}
}

func TestFTSInsertStatusDeleteSync(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	ins, err := s.UpsertEntries(ctx, fid, []*core.Entry{
		ftsEntry(fid, "g1", "Kubernetes guide", "<p>pods and nodes</p>", ""),
	})
	if err != nil || len(ins) != 1 {
		t.Fatalf("upsert: ins=%d err=%v", len(ins), err)
	}
	id := int64(ins[0].ID)

	if got := ftsIDs(t, s, "kubernetes"); len(got) != 1 || got[0] != id {
		t.Fatalf("title not indexed: %v", got)
	}
	if got := ftsIDs(t, s, "pods"); len(got) != 1 || got[0] != id {
		t.Fatalf("content not indexed: %v", got)
	}

	// Status toggle changes no indexed column -> FTS unchanged (proves AFTER UPDATE OF).
	if err := s.SetStatus(ctx, core.DefaultUserID, []core.ID{ins[0].ID}, core.StatusRead); err != nil {
		t.Fatal(err)
	}
	if got := ftsIDs(t, s, "kubernetes"); len(got) != 1 {
		t.Fatalf("status toggle disturbed FTS: %v", got)
	}

	// Delete removes from FTS.
	if err := s.DeleteEntry(ctx, core.DefaultUserID, ins[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := ftsIDs(t, s, "kubernetes"); len(got) != 0 {
		t.Fatalf("deleted entry still in FTS: %v", got)
	}
}

func TestFTSIndexesSummary(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	// Description-only feed: body lives in summary, content empty.
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{
		ftsEntry(fid, "g1", "Title", "", "<p>a post about rabbits</p>"),
	}); err != nil {
		t.Fatal(err)
	}
	if got := ftsIDs(t, s, "rabbits"); len(got) != 1 {
		t.Fatalf("summary not indexed (description-only feeds unsearchable): %v", got)
	}
}

func TestFTSUpdateReflectsNewText(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	e := ftsEntry(fid, "g1", "old kubernetes", "body", "")
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{e}); err != nil {
		t.Fatal(err)
	}
	// Re-poll same guid with changed hash + text -> UpdateEntryContent fires entries_au.
	e2 := ftsEntry(fid, "g1", "new docker", "body", "")
	e2.Hash = "h-changed"
	if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{e2}); err != nil {
		t.Fatal(err)
	}
	if got := ftsIDs(t, s, "docker"); len(got) != 1 {
		t.Fatalf("new text not indexed: %v", got)
	}
	if got := ftsIDs(t, s, "kubernetes"); len(got) != 0 {
		t.Fatalf("old text still indexed after update: %v", got)
	}
}

func TestFTSBackfillsPreexistingRows(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "b.db") + "?_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(MigrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	// Migrate to BEFORE the FTS migration, insert an entry, THEN apply 0004.
	if err := goose.UpTo(db, "migrations", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feeds(user_id,feed_url,next_check_at,created_at,updated_at) VALUES(1,'https://f/x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries(user_id,feed_id,guid,title,content,summary,published_at,created_at)
	                      VALUES(1,1,'g','Backfilled kubernetes post','body','',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 4); err != nil {
		t.Fatal(err)
	}
	var rowid int64
	if err := db.QueryRowContext(ctx, `SELECT rowid FROM entries_fts WHERE entries_fts MATCH 'kubernetes'`).Scan(&rowid); err != nil {
		t.Fatalf("pre-existing entry not back-filled into FTS: %v", err)
	}
	if rowid != 1 {
		t.Fatalf("rowid=%d want 1", rowid)
	}
}
