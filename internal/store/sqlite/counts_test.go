package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// TestCounts exercises the five global count methods used by the metrics
// collector: CountUsers, CountFeeds, CountEntries, CountDueFeeds, and
// CountDueExtractions. Rows are seeded on both sides of the `now` cutoff for
// the two due-counts, and CountDueFeeds/CountDueExtractions are checked to
// mirror ListDueFeeds/ListPendingExtractions's WHERE clauses exactly
// (disabled feeds and future next_check_at/next_extract_at excluded).
func TestCounts(t *testing.T) {
	st, ctx := newTestStore(t), context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	// migration 0001 seeds exactly one user (id=1, "ben").
	users, err := st.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if users != 1 {
		t.Fatalf("CountUsers: want 1, got %d", users)
	}

	// feedA: due now (enabled, next_check_at in the past).
	feedA := mustFeed(t, st, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://a.test/feed", NextCheckAt: past,
		CreatedAt: now, UpdatedAt: now, FetchFullContent: true,
	})
	// feedB: not due yet (next_check_at in the future).
	mustFeed(t, st, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://b.test/feed", NextCheckAt: future,
		CreatedAt: now, UpdatedAt: now,
	})
	// feedC: would be due by time, but disabled -- must be excluded, mirroring
	// ListDueFeeds's `disabled = 0` clause.
	feedC := mustFeed(t, st, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://c.test/feed", NextCheckAt: past,
		CreatedAt: now, UpdatedAt: now, Disabled: true,
	})

	feeds, err := st.CountFeeds(ctx)
	if err != nil {
		t.Fatalf("CountFeeds: %v", err)
	}
	if feeds != 3 {
		t.Fatalf("CountFeeds: want 3, got %d", feeds)
	}

	due, err := st.CountDueFeeds(ctx, now)
	if err != nil {
		t.Fatalf("CountDueFeeds: %v", err)
	}
	if due != 1 {
		t.Fatalf("CountDueFeeds: want 1 (only feedA), got %d", due)
	}
	// Drift guard: CountDueFeeds must always agree with the actual dispatch
	// query it mirrors, not just happen to match on this fixture.
	listedDue, err := st.ListDueFeeds(ctx, now, 1000)
	if err != nil {
		t.Fatalf("ListDueFeeds: %v", err)
	}
	if int64(len(listedDue)) != due {
		t.Fatalf("CountDueFeeds=%d but ListDueFeeds returned %d rows (drift)", due, len(listedDue))
	}
	for _, f := range listedDue {
		if f.ID == feedC {
			t.Fatalf("disabled feedC must not appear in ListDueFeeds")
		}
	}

	// e1/e2 land on feedA (FetchFullContent=true), so both are inserted
	// extract_state='pending' with next_extract_at = their own CreatedAt:
	// e1 is due for extraction, e2 is not yet.
	e1 := mkEntry(feedA, "e1", past)
	e1.CreatedAt = past
	e2 := mkEntry(feedA, "e2", future)
	e2.CreatedAt = future
	if _, err := st.UpsertEntries(ctx, feedA, []*core.Entry{e1, e2}); err != nil {
		t.Fatalf("upsert feedA entries: %v", err)
	}

	// e3 lands on a feed without full-content fetching, so it stays
	// extract_state='none' and must not count towards CountDueExtractions.
	feedD := mustFeed(t, st, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://d.test/feed", NextCheckAt: future,
		CreatedAt: now, UpdatedAt: now,
	})
	e3 := mkEntry(feedD, "e3", now)
	if _, err := st.UpsertEntries(ctx, feedD, []*core.Entry{e3}); err != nil {
		t.Fatalf("upsert feedD entry: %v", err)
	}

	entries, err := st.CountEntries(ctx)
	if err != nil {
		t.Fatalf("CountEntries: %v", err)
	}
	if entries != 3 {
		t.Fatalf("CountEntries: want 3, got %d", entries)
	}

	dueExtractions, err := st.CountDueExtractions(ctx, now)
	if err != nil {
		t.Fatalf("CountDueExtractions: %v", err)
	}
	if dueExtractions != 1 {
		t.Fatalf("CountDueExtractions: want 1 (only e1), got %d", dueExtractions)
	}
	// Drift guard: CountDueExtractions must always agree with the actual
	// dispatch query it mirrors, not just happen to match on this fixture.
	listedExtractions, err := st.ListPendingExtractions(ctx, now, 1000)
	if err != nil {
		t.Fatalf("ListPendingExtractions: %v", err)
	}
	if int64(len(listedExtractions)) != dueExtractions {
		t.Fatalf("CountDueExtractions=%d but ListPendingExtractions returned %d rows (drift)", dueExtractions, len(listedExtractions))
	}
}
