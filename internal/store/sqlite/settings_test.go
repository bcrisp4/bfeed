package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestAppSettingsRoundtrip(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := New(db)
	ctx := context.Background()

	if _, err := st.GetSetting(ctx, "k"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing key: want ErrNotFound, got %v", err)
	}
	if err := st.PutSetting(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.GetSetting(ctx, "k"); v != "v1" {
		t.Fatalf("got %q want v1", v)
	}
	if err := st.PutSetting(ctx, "k", "v2"); err != nil { // upsert overwrites
		t.Fatal(err)
	}
	if v, _ := st.GetSetting(ctx, "k"); v != "v2" {
		t.Fatalf("got %q want v2", v)
	}
}
