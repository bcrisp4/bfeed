package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/bcrisp4/bfeed/internal/core"
)

// F4: the FTS index holds a plain-text projection of content/summary, so searches
// match visible words only — not tag names, attribute names, or URLs in the markup.
func TestFTSIndexesVisibleTextNotMarkup(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	ins, err := s.UpsertEntries(ctx, fid, []*core.Entry{
		ftsEntry(fid, "g1", "Title", `<a href="https://example.com/page">searchword</a>`, `<p>summaryword visible</p>`),
	})
	if err != nil || len(ins) != 1 {
		t.Fatalf("upsert: ins=%d err=%v", len(ins), err)
	}
	id := int64(ins[0].ID)

	if got := ftsIDs(t, s, "searchword"); len(got) != 1 || got[0] != id {
		t.Fatalf("visible content text not indexed: %v", got)
	}
	if got := ftsIDs(t, s, "summaryword"); len(got) != 1 || got[0] != id {
		t.Fatalf("visible summary text not indexed: %v", got)
	}
	if got := ftsIDs(t, s, "https"); len(got) != 0 {
		t.Fatalf("URL scheme in markup was indexed: %v", got)
	}
	if got := ftsIDs(t, s, "href"); len(got) != 0 {
		t.Fatalf("attribute name in markup was indexed: %v", got)
	}
}

// ftsCount returns how many rows a raw-db FTS query matches.
func ftsCount(t *testing.T, db *sql.DB, match string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entries_fts WHERE entries_fts MATCH ?`, match).Scan(&n); err != nil {
		t.Fatalf("fts count %q: %v", match, err)
	}
	return n
}

// F4 upgrade path: pre-0013 rows (raw HTML in content) become searchable by visible
// text and NOT by markup after the migration's Go backfill. An image-only entry
// (no visible text) must not stall the keyset backfill loop.
func TestFTSBackfillPlainTextOnUpgrade(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "u.db") + "?_pragma=foreign_keys(ON)"
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
	// Migrate to just BEFORE the plain-text FTS migration (0013).
	if err := goose.UpTo(db, "migrations", 12); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feeds(user_id,feed_url,next_check_at,created_at,updated_at) VALUES(1,'https://f/x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries(user_id,feed_id,guid,title,content,summary,published_at,created_at,hash)
	                      VALUES(1,1,'g1','T',?,'',0,0,'h1')`,
		`<a href="https://example.com/page">visibleword</a>`); err != nil {
		t.Fatal(err)
	}
	// Image-only entry: PlainText() is empty; must not loop forever in the backfill.
	if _, err := db.Exec(`INSERT INTO entries(user_id,feed_id,guid,title,content,summary,published_at,created_at,hash)
	                      VALUES(1,1,'g2','',?,'',0,0,'h2')`,
		`<img src="https://example.com/pic.png">`); err != nil {
		t.Fatal(err)
	}

	// Full migrate: applies 0013 then runs the Go backfill + rebuild.
	if err := migrate(db); err != nil {
		t.Fatalf("migrate to 0013 + backfill: %v", err)
	}

	if got := ftsCount(t, db, "visibleword"); got != 1 {
		t.Fatalf("pre-existing row not searchable by visible text: %d", got)
	}
	if got := ftsCount(t, db, "https"); got != 0 {
		t.Fatalf("markup URL still searchable after backfill: %d", got)
	}
	if got := ftsCount(t, db, "href"); got != 0 {
		t.Fatalf("attribute name still searchable after backfill: %d", got)
	}

	// Idempotent: a second migrate (e.g. next boot) must be a no-op (flag set) and
	// leave the index consistent.
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var flag string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key='fts_backfill_0013'`).Scan(&flag); err != nil {
		t.Fatalf("backfill completion flag not set: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO entries_fts(entries_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("FTS integrity-check failed: %v", err)
	}
}
