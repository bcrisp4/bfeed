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

// B7 #1: feeds.id is AUTOINCREMENT, so a deleted feed's id is never recycled — a
// stale background subscribe/refresh goroutine can no longer land the old feed's
// URL/metadata/entries on a new feed that reused the id.
func TestFeedIDNotReusedAfterDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	mk := func(url string) core.ID {
		id, err := s.CreateFeed(ctx, &core.Feed{
			UserID: core.DefaultUserID, FeedURL: url, NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateFeed(%s): %v", url, err)
		}
		return id
	}
	mk("https://a.test/f")
	top := mk("https://b.test/f") // highest id
	if err := s.DeleteFeed(ctx, core.DefaultUserID, top); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}
	reused := mk("https://c.test/f")
	if reused == top {
		t.Fatalf("AUTOINCREMENT must not reuse deleted id %d", top)
	}
}

// B7 #1: the AUTOINCREMENT rebuild (migration 0010) must preserve every existing
// feed, its entries, and its tombstones — a DROP TABLE with foreign_keys ON would
// otherwise cascade-delete the entries.
func TestMigration0010PreservesData(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "u.db") + "?_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	goose.SetBaseFS(MigrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	// Seed at the pre-AUTOINCREMENT schema (version 9).
	if err := goose.UpTo(db, "migrations", 9); err != nil {
		t.Fatalf("up to 9: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO feeds (id,user_id,feed_url,title,etag,next_check_at,created_at,updated_at) VALUES (5,1,'https://x/f','X','etag5',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO entries (user_id,feed_id,guid,published_at,created_at) VALUES (1,5,'g1',0,0),(1,5,'g2',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tombstones (feed_id,guid,deleted_at) VALUES (5,'gone',0)`); err != nil {
		t.Fatal(err)
	}
	// Apply the rebuild.
	if err := goose.UpTo(db, "migrations", 10); err != nil {
		t.Fatalf("up to 10: %v", err)
	}
	var nf, ne, nt int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM feeds`).Scan(&nf)
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM entries`).Scan(&ne)
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM tombstones`).Scan(&nt)
	if nf != 1 || ne != 2 || nt != 1 {
		t.Fatalf("data lost across rebuild: feeds=%d entries=%d tombstones=%d (want 1,2,1)", nf, ne, nt)
	}
	var title, etag string
	_ = db.QueryRowContext(ctx, `SELECT title,etag FROM feeds WHERE id=5`).Scan(&title, &etag)
	if title != "X" || etag != "etag5" {
		t.Fatalf("feed metadata lost: title=%q etag=%q", title, etag)
	}
	// AUTOINCREMENT is now live: a fresh insert must not reuse id 5.
	if _, err := db.ExecContext(ctx, `DELETE FROM feeds WHERE id=5`); err != nil {
		t.Fatal(err)
	}
	var newID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO feeds (user_id,feed_url,next_check_at,created_at,updated_at) VALUES (1,'https://y/f',0,0,0) RETURNING id`).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	if newID <= 5 {
		t.Fatalf("expected AUTOINCREMENT id > 5, got %d", newID)
	}
}

// B7 #4/#5: UpdateFeed carries a feed_url CAS guard, so a poll that completes after
// a concurrent URL edit is a no-op instead of resurrecting the old URL's cleared
// etag/last_modified.
func TestUpdateFeedFeedURLGuardNoOps(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	id, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://old.test/f", Title: "Old",
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	// A poll dispatched with the OLD URL snapshot.
	snapshot, _ := s.GetFeed(ctx, core.DefaultUserID, id)

	// Meanwhile the user edits the URL (clears validators, resets schedule).
	if err := s.SetFeedURL(ctx, core.DefaultUserID, id, "https://new.test/f", now.Add(time.Hour)); err != nil {
		t.Fatalf("SetFeedURL: %v", err)
	}

	// The stale poll writes back the old URL's metadata + validators.
	snapshot.Title = "StalePollTitle"
	snapshot.ETag = `"stale"`
	snapshot.LastModified = "Wed, 01 Jan 2025 00:00:00 GMT"
	snapshot.NextCheckAt = now.Add(24 * time.Hour)
	if err := s.UpdateFeed(ctx, snapshot); err != nil {
		t.Fatalf("UpdateFeed: %v", err)
	}

	got, _ := s.GetFeed(ctx, core.DefaultUserID, id)
	if got.FeedURL != "https://new.test/f" {
		t.Fatalf("feed_url = %q, want the edited URL", got.FeedURL)
	}
	if got.ETag != "" || got.LastModified != "" {
		t.Errorf("stale poll resurrected validators: etag=%q last_modified=%q", got.ETag, got.LastModified)
	}
	if got.Title == "StalePollTitle" {
		t.Errorf("stale poll clobbered title after URL edit")
	}
	if !got.NextCheckAt.Equal(now.Add(time.Hour)) {
		t.Errorf("stale poll overwrote next_check_at: %v", got.NextCheckAt)
	}
}

// B7 #3: new entries take extract_state from the feed's CURRENT fetch_full_content,
// read inside the upsert transaction — not the caller-supplied Entry.ExtractState.
func TestUpsertEntriesDerivesExtractStateFromLiveFeed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	id, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://x/f", FetchFullContent: true,
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	// Caller passes ExtractState none (as a poll snapshot taken before the toggle
	// would); the store must override it to pending because the feed wants full content.
	e := mkEntry(id, "g", now)
	e.ExtractState = core.ExtractNone
	if _, err := s.UpsertEntries(ctx, id, []*core.Entry{e}); err != nil {
		t.Fatalf("UpsertEntries: %v", err)
	}
	pend, err := s.ListPendingExtractions(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListPendingExtractions: %v", err)
	}
	if len(pend) != 1 {
		t.Fatalf("want 1 pending entry from a full-content feed, got %d", len(pend))
	}
}

// B7 #1 (entries tail): SetEntryContent guards on extract_state = 'pending', so a
// stale Scraper writing onto an entry that is no longer pending is a no-op.
func TestSetEntryContentGuardsOnPending(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	id, err := s.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://x/f", FetchFullContent: true,
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	ins, err := s.UpsertEntries(ctx, id, []*core.Entry{mkEntry(id, "g", now)})
	if err != nil || len(ins) != 1 {
		t.Fatalf("UpsertEntries: %v (n=%d)", err, len(ins))
	}
	eid := ins[0].ID
	// First scrape wins (row is pending).
	if err := s.SetEntryContent(ctx, eid, "<p>real</p>"); err != nil {
		t.Fatalf("SetEntryContent: %v", err)
	}
	// A second (stale) scrape must NOT overwrite: the row is now 'done', not pending.
	if err := s.SetEntryContent(ctx, eid, "<p>stale</p>"); err != nil {
		t.Fatalf("SetEntryContent (stale): %v", err)
	}
	got, _ := s.GetEntry(ctx, core.DefaultUserID, eid)
	if got.Content != "<p>real</p>" {
		t.Errorf("stale scrape overwrote settled content: %q", got.Content)
	}
}
