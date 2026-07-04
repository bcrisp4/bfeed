package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/pressly/goose/v3"
	// modernc.org/sqlite registers the pure-Go "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/bcrisp4/bfeed/internal/core"
)

//go:embed migrations/*.sql
var MigrationsFS embed.FS

// Open returns a migrated, ready single-writer SQLite pool.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"+
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"+
		"&_pragma=temp_store(MEMORY)&_pragma=cache_size(-8000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer; removes SQLITE_BUSY at O(1) users
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	goose.SetBaseFS(MigrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	if err := backfillSearchText(db); err != nil {
		return fmt.Errorf("fts backfill: %w", err)
	}
	return nil
}

// backfillSearchText populates entries.content_text/summary_text (the plain-text
// FTS projection added by 0013) for rows that predate the migration, then rebuilds
// the index. Pure SQL cannot strip HTML tags, so this runs in Go once. It is guarded
// by an app_settings flag written only after the rebuild commits, so it runs exactly
// once and is crash-safe (a crash before the flag re-runs the whole thing next boot).
// Batched because the single-writer pool (SetMaxOpenConns(1)) would deadlock if an
// UPDATE were issued while a SELECT's rows were still open on the one connection.
func backfillSearchText(db *sql.DB) error {
	const flag = "fts_backfill_0013"
	var done string
	err := db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, flag).Scan(&done)
	if err == nil {
		return nil // already back-filled
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	const batch = 500
	type row struct {
		id               int64
		content, summary string
	}
	// Keyset by id (not a content_text='' predicate): an image-only entry has
	// PlainText(content)=="", so a WHERE content_text='' loop would re-select it
	// forever. Advancing past each id processes every row exactly once.
	var lastID int64
	for {
		rows, err := db.Query(
			`SELECT id, content, summary FROM entries WHERE id > ? ORDER BY id LIMIT ?`,
			lastID, batch)
		if err != nil {
			return err
		}
		var batchRows []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.content, &r.summary); err != nil {
				_ = rows.Close()
				return err
			}
			batchRows = append(batchRows, r)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close() // release the single connection before issuing UPDATEs

		if len(batchRows) == 0 {
			break
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, r := range batchRows {
			if _, err := tx.Exec(
				`UPDATE entries SET content_text = ?, summary_text = ? WHERE id = ?`,
				core.PlainText(r.content), core.PlainText(r.summary), r.id); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		lastID = batchRows[len(batchRows)-1].id
	}

	// Reconstruct the index from the now-populated text columns.
	if _, err := db.Exec(`INSERT INTO entries_fts(entries_fts) VALUES('rebuild')`); err != nil {
		return err
	}
	// Create the AFTER UPDATE re-index trigger only now — 0013 deliberately omits it
	// so it can't fire an external-content 'delete' against the empty index mid-backfill
	// (which corrupts it). IF NOT EXISTS keeps a crash-retry (flag not yet set) safe.
	if _, err := db.Exec(`
CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE OF title, content_text, summary_text ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content_text, summary_text)
  VALUES('delete', old.id, old.title, old.content_text, old.summary_text);
  INSERT INTO entries_fts(rowid, title, content_text, summary_text)
  VALUES (new.id, new.title, new.content_text, new.summary_text);
END`); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO app_settings(key, value) VALUES(?, '1')`, flag)
	return err
}
