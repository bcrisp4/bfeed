package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func seedFeed(t *testing.T, s *Store) core.ID {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	id, err := s.CreateFeed(context.Background(), &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://f.test/x", NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mkEntry(feedID core.ID, guid string, pub time.Time) *core.Entry {
	return &core.Entry{
		UserID: core.DefaultUserID, FeedID: feedID, GUID: guid, URL: "https://f.test/" + guid,
		Title: "T-" + guid, Content: "<p>c</p>", PublishedAt: pub, Status: core.StatusUnread,
		CreatedAt: pub, Hash: "h-" + guid,
	}
}

func TestUpsertInsertsThenDedupes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	p := time.Unix(1_700_000_100, 0).UTC()
	ins, err := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, "g1", p)})
	if err != nil || len(ins) != 1 {
		t.Fatalf("first upsert: ins=%d err=%v", len(ins), err)
	}
	// Same guid + same hash → no new insert.
	ins2, err := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, "g1", p)})
	if err != nil || len(ins2) != 0 {
		t.Fatalf("dedupe upsert: ins=%d err=%v", len(ins2), err)
	}
}

func TestUpsertSkipsTombstoned(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	p := time.Unix(1_700_000_100, 0).UTC()
	ins, _ := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, "g1", p)})
	if err := s.DeleteEntry(ctx, core.DefaultUserID, ins[0].ID); err != nil {
		t.Fatal(err)
	}
	// Re-poll the same guid: tombstone must prevent resurrection.
	ins2, err := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, "g1", p)})
	if err != nil || len(ins2) != 0 {
		t.Fatalf("tombstoned guid resurrected: ins=%d err=%v", len(ins2), err)
	}
}

func TestListEntriesKeysetAndStatusFilter(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	for i := 0; i < 5; i++ {
		p := time.Unix(int64(1_700_000_100+i), 0).UTC()
		if _, err := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, string(rune('a'+i)), p)}); err != nil {
			t.Fatal(err)
		}
	}
	unread := core.StatusUnread
	page1, cur, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Status: &unread, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || cur == nil {
		t.Fatalf("page1 len=%d cur=%v", len(page1), cur)
	}
	// Newest first.
	if !page1[0].PublishedAt.After(page1[1].PublishedAt) {
		t.Fatal("not ordered newest-first")
	}
	page2, _, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Status: &unread, Limit: 2, Cursor: cur})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || !page1[1].PublishedAt.After(page2[0].PublishedAt) {
		t.Fatalf("keyset page2 wrong: %+v", page2)
	}
}

func TestSetStatusAndStarred(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)
	ins, _ := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, "g1", time.Unix(1_700_000_100, 0).UTC())})
	id := ins[0].ID
	if err := s.SetStatus(ctx, core.DefaultUserID, []core.ID{id}, core.StatusRead); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStarred(ctx, core.DefaultUserID, []core.ID{id}, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetEntry(ctx, core.DefaultUserID, id)
	if got.Status != core.StatusRead || !got.Starred || got.ReadAt == nil {
		t.Fatalf("status/star/readAt not applied: %+v", got)
	}
}

func TestHotListUsesIndex(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rows, err := s.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN SELECT id FROM entries WHERE user_id=1 AND status='unread' ORDER BY published_at DESC, id DESC LIMIT 50`)
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
		t.Fatalf("hot list query sorts in memory:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_entries_user_status_pub") {
		t.Fatalf("hot list query not using covering index:\n%s", plan)
	}
}
