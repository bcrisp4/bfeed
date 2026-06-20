package sqlite

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/store/sqlite/sqlc"
)

// Store implements core.Store over a single-writer SQLite pool.
type Store struct {
	db *sql.DB
	q  *sqlc.Queries
}

func New(db *sql.DB) *Store { return &Store{db: db, q: sqlc.New(db)} }

var _ core.Store = (*Store)(nil)

func toUnix(t time.Time) int64   { return t.UTC().Unix() }
func fromUnix(s int64) time.Time { return time.Unix(s, 0).UTC() }

func nullUnix(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UTC().Unix(), Valid: true}
}

func ptrUnix(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0).UTC()
	return &t
}

func nullID(id *core.ID) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*id), Valid: true}
}

func ptrID(n sql.NullInt64) *core.ID {
	if !n.Valid {
		return nil
	}
	id := core.ID(n.Int64)
	return &id
}

// mapErr converts driver errors to core sentinels.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return core.ErrConflict
	}
	if strings.Contains(msg, "FOREIGN KEY constraint failed") {
		return core.ErrConflict
	}
	return err
}
