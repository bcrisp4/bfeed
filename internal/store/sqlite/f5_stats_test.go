package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func seedFeedURL(t *testing.T, s *Store, url string) core.ID {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	id, err := s.CreateFeed(context.Background(), &core.Feed{
		UserID: core.DefaultUserID, FeedURL: url, NextCheckAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedStatus(t *testing.T, s *Store, fid core.ID, guid string, st core.EntryStatus) {
	t.Helper()
	ctx := context.Background()
	p := time.Unix(1_700_000_100, 0).UTC()
	ins, err := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, guid, p)})
	if err != nil || len(ins) != 1 {
		t.Fatalf("seed %s: %v", guid, err)
	}
	if st != core.StatusUnread {
		if err := s.SetStatus(ctx, core.DefaultUserID, []core.ID{ins[0].ID}, st); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnreadCount(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f1 := seedFeedURL(t, s, "https://f.test/1")
	f2 := seedFeedURL(t, s, "https://f.test/2")
	seedStatus(t, s, f1, "a", core.StatusUnread)
	seedStatus(t, s, f1, "b", core.StatusRead)
	seedStatus(t, s, f2, "c", core.StatusUnread)
	seedStatus(t, s, f2, "d", core.StatusUnread)

	n, err := s.UnreadCount(ctx, core.DefaultUserID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("UnreadCount = %d, want 3", n)
	}
}

func TestFeedEntryStatsByID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f1 := seedFeedURL(t, s, "https://f.test/1")
	f2 := seedFeedURL(t, s, "https://f.test/2")
	seedStatus(t, s, f1, "a", core.StatusUnread)
	seedStatus(t, s, f1, "b", core.StatusRead)
	seedStatus(t, s, f1, "e", core.StatusUnread)
	seedStatus(t, s, f2, "c", core.StatusUnread) // other feed must not leak in

	st, err := s.FeedEntryStatsByID(ctx, core.DefaultUserID, f1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 3 || st.Unread != 2 {
		t.Fatalf("feed stats = %+v, want {Total:3 Unread:2}", st)
	}
}

func TestFeedStatsUsesIndex(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rows, err := s.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN SELECT COUNT(*), COUNT(*) FILTER (WHERE status='unread') FROM entries WHERE user_id=1 AND feed_id=7`)
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
	if strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("per-feed stats sorts in memory:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_entries_feed_pub") {
		t.Fatalf("per-feed stats not using idx_entries_feed_pub:\n%s", plan)
	}
}
