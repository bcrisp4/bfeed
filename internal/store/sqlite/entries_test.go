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

func TestHistoryUsesIndex(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rows, err := s.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN SELECT id FROM entries WHERE user_id=1 AND read_at IS NOT NULL ORDER BY read_at DESC, id DESC LIMIT 50`)
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
		t.Fatalf("history query sorts in memory:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_entries_readhist") {
		t.Fatalf("history query not using partial index:\n%s", plan)
	}
}

func TestHistoryOrderAndKeyset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fid := seedFeed(t, s)

	// Insert 5 entries (ids increase with insertion order).
	ids := make([]core.ID, 5)
	for i := 0; i < 5; i++ {
		p := time.Unix(int64(1_700_000_100+i), 0).UTC()
		ins, err := s.UpsertEntries(ctx, fid, []*core.Entry{mkEntry(fid, string(rune('a'+i)), p)})
		if err != nil || len(ins) != 1 {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids[i] = ins[0].ID
	}

	// Set read_at directly (SetStatus uses wall-clock; we need known values).
	// id[1] and id[2] tie on read_at=1002 -> id DESC must order id2 before id1.
	// id[4] stays unread (read_at NULL) -> excluded from history.
	for id, ra := range map[core.ID]int64{ids[0]: 1000, ids[1]: 1002, ids[2]: 1002, ids[3]: 1004} {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE entries SET status='read', read_at=? WHERE id=?`, ra, int64(id)); err != nil {
			t.Fatal(err)
		}
	}

	want := []core.ID{ids[3], ids[2], ids[1], ids[0]} // read_at desc, id desc tiebreak

	// Page 1.
	page1, cur, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Order: core.OrderReadAtDesc, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].ID != want[0] || page1[1].ID != want[1] {
		t.Fatalf("page1 ids = %d,%d want %d,%d", idOf(page1, 0), idOf(page1, 1), want[0], want[1])
	}
	if cur == nil || cur.Key != 1002 || cur.ID != ids[2] {
		t.Fatalf("cursor = %+v", cur)
	}

	// Page 2 via keyset.
	page2, _, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Order: core.OrderReadAtDesc, Limit: 2, Cursor: cur})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID != want[2] || page2[1].ID != want[3] {
		t.Fatalf("page2 ids = %d,%d want %d,%d", idOf(page2, 0), idOf(page2, 1), want[2], want[3])
	}

	// The unread entry (ids[4]) must never appear.
	for _, e := range append(page1, page2...) {
		if e.ID == ids[4] {
			t.Fatal("unread entry leaked into history")
		}
	}
}

func mustFeed(t *testing.T, s *Store, f *core.Feed) core.ID {
	t.Helper()
	id, err := s.CreateFeed(context.Background(), f)
	if err != nil {
		t.Fatalf("mustFeed: %v", err)
	}
	return id
}

func TestExtractionStateLifecycle(t *testing.T) {
	st, ctx := newTestStore(t), context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	feedID := mustFeed(t, st, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://x.example/feed", NextCheckAt: now, CreatedAt: now, UpdatedAt: now, FetchFullContent: true})

	// Two entries inserted pending (newest first by published_at).
	older := &core.Entry{UserID: core.DefaultUserID, FeedID: feedID, GUID: "a", URL: "https://x.example/a", PublishedAt: now.Add(-2 * time.Hour), CreatedAt: now, Hash: "h1", ExtractState: core.ExtractPending}
	newer := &core.Entry{UserID: core.DefaultUserID, FeedID: feedID, GUID: "b", URL: "https://x.example/b", PublishedAt: now.Add(-1 * time.Hour), CreatedAt: now, Hash: "h2", ExtractState: core.ExtractPending}
	if _, err := st.UpsertEntries(ctx, feedID, []*core.Entry{older, newer}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	pend, err := st.ListPendingExtractions(ctx, now, 10)
	if err != nil || len(pend) != 2 {
		t.Fatalf("want 2 pending, got %d err=%v", len(pend), err)
	}
	if pend[0].URL != "https://x.example/b" { // freshest first
		t.Fatalf("want newest first, got %s", pend[0].URL)
	}

	// SetEntryContent → done, leaves pending list at 1.
	if err := st.SetEntryContent(ctx, pend[0].ID, "<p>full</p>"); err != nil {
		t.Fatalf("SetEntryContent: %v", err)
	}
	got, _ := st.GetEntry(ctx, core.DefaultUserID, pend[0].ID)
	if got.Content != "<p>full</p>" || got.ExtractState != core.ExtractDone {
		t.Fatalf("want done+content, got %q %q", got.Content, got.ExtractState)
	}

	// UpdateExtractState retry: future next_extract_at hides it from the due list.
	future := now.Add(time.Hour)
	if err := st.UpdateExtractState(ctx, pend[1].ID, core.ExtractPending, 1, &future); err != nil {
		t.Fatalf("UpdateExtractState: %v", err)
	}
	// Verify attempts persisted.
	rb, err := st.GetEntry(ctx, core.DefaultUserID, pend[1].ID)
	if err != nil {
		t.Fatalf("GetEntry after UpdateExtractState: %v", err)
	}
	if rb.ExtractAttempts != 1 {
		t.Fatalf("ExtractAttempts: want 1, got %d", rb.ExtractAttempts)
	}
	if p, _ := st.ListPendingExtractions(ctx, now, 10); len(p) != 0 {
		t.Fatalf("want 0 due (backoff), got %d", len(p))
	}
	if p, _ := st.ListPendingExtractions(ctx, future, 10); len(p) != 1 {
		t.Fatalf("want 1 due after backoff, got %d", len(p))
	}
}

func TestListPendingExtractionsUsesPartialIndex(t *testing.T) {
	st, ctx := newTestStore(t), context.Background()
	rows, err := st.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN SELECT * FROM entries WHERE extract_state='pending' AND next_extract_at <= 0 ORDER BY published_at DESC, id DESC LIMIT 50`)
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
	if !strings.Contains(plan, "idx_entries_pending") {
		t.Fatalf("expected idx_entries_pending, plan:\n%s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("unexpected temp b-tree, plan:\n%s", plan)
	}
}

func idOf(es []*core.Entry, i int) core.ID {
	if i < len(es) {
		return es[i].ID
	}
	return -1
}

func TestListEntriesByCategoryAndUncategorised(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Unix(1_700_000_100, 0).UTC()
	catID, _ := s.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fCat, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a.test/f", CategoryID: &catID, NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	fUncat, _ := s.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	if _, err := s.UpsertEntries(ctx, fCat, []*core.Entry{mkEntry(fCat, "c1", now)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertEntries(ctx, fUncat, []*core.Entry{mkEntry(fUncat, "u1", now)}); err != nil {
		t.Fatal(err)
	}

	inCat, _, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{CategoryID: &catID})
	if err != nil {
		t.Fatal(err)
	}
	if len(inCat) != 1 || inCat[0].FeedID != fCat {
		t.Fatalf("category filter got %d entries (want 1 from fCat)", len(inCat))
	}

	uncat, _, err := s.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{Uncategorised: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(uncat) != 1 || uncat[0].FeedID != fUncat {
		t.Fatalf("uncategorised filter got %d entries (want 1 from fUncat)", len(uncat))
	}
}

func TestCategoryStreamUsesIndexNoSort(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rows, err := s.db.QueryContext(ctx,
		`EXPLAIN QUERY PLAN SELECT e.id FROM entries e JOIN feeds f ON e.feed_id = f.id
		 WHERE e.user_id = 1 AND f.category_id = 1 ORDER BY e.published_at DESC, e.id DESC LIMIT 50`)
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
		t.Fatalf("category stream sorts in memory:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_entries_user_pub") {
		t.Fatalf("category stream not using idx_entries_user_pub:\n%s", plan)
	}
}
