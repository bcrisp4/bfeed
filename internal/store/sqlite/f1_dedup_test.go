package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// F1: EntryDedup reports existing (guid->hash) and tombstoned GUIDs for a feed so
// the poll pipeline sanitises only new/changed, non-tombstoned entries.
func TestEntryDedup(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	p := time.Unix(1_700_000_100, 0).UTC()

	live := mkEntry(fid, "live", p)
	live.Hash = "hlive"
	gone := mkEntry(fid, "gone", p)
	ins, err := s.UpsertEntries(ctx, fid, []*core.Entry{live, gone})
	if err != nil || len(ins) != 2 {
		t.Fatalf("seed: ins=%d err=%v", len(ins), err)
	}
	// Tombstone "gone".
	var goneID core.ID
	for _, e := range ins {
		if e.GUID == "gone" {
			goneID = e.ID
		}
	}
	if err := s.DeleteEntry(ctx, core.DefaultUserID, goneID); err != nil {
		t.Fatal(err)
	}

	existing, tombstoned, err := s.EntryDedup(ctx, fid, []string{"live", "gone", "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if existing["live"] != "hlive" {
		t.Fatalf("existing[live] = %q, want hlive", existing["live"])
	}
	if _, ok := existing["fresh"]; ok {
		t.Fatalf("fresh guid must be absent from existing")
	}
	if _, ok := existing["gone"]; ok {
		t.Fatalf("tombstoned guid must not appear as existing")
	}
	if !tombstoned["gone"] {
		t.Fatalf("gone guid must be reported tombstoned")
	}
	if tombstoned["live"] || tombstoned["fresh"] {
		t.Fatalf("live/fresh must not be tombstoned")
	}
}
