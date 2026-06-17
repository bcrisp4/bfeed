package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndSeedsUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE id = 1`).Scan(&n); err != nil {
		t.Fatalf("query users: %v", err)
	}
	if n != 1 {
		t.Fatalf("seeded users = %d, want 1", n)
	}

	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if fk != 1 {
		t.Fatal("foreign_keys must be ON")
	}
}
