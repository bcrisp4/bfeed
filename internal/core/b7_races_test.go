package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// B7 #2: a freshly-created pending row is NOT immediately poll-due. The web layer
// resolves it in a background goroutine that owns the first ingest; making it due at
// once lets the Poller dispatch it concurrently and clobber that result. The row is
// scheduled one MinInterval out, so the Poller only touches it if the resolve died.
func TestCreateSubscriptionSchedulesGracePeriod(t *testing.T) {
	store := coretest.NewMemStore()
	svc, clk := newFeedSvc(store, coretest.StubFetcher{}, coretest.StubParser{})
	f, err := svc.CreateSubscription(context.Background(), core.DefaultUserID, "https://x.test/feed.xml", nil, false)
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	want := clk.Now().Add(15 * time.Minute) // MinInterval from newFeedSvc
	if !f.NextCheckAt.Equal(want) {
		t.Errorf("pending row NextCheckAt = %v, want now+MinInterval %v", f.NextCheckAt, want)
	}
	if f.NextCheckAt.Equal(clk.Now()) {
		t.Error("pending row must not be immediately due (would race the background resolve)")
	}
}

// B7 #4/#5: a poll that finishes after the user edits the feed's URL must not write
// the old URL's metadata/validators back. recordSuccess routes through UpdateFeed,
// whose feed_url CAS makes the stale write a no-op once SetFeedURL changed the URL.
func TestPollAfterURLEditDoesNotClobber(t *testing.T) {
	ctx := context.Background()
	store := coretest.NewMemStore()
	// A parser that always succeeds so PollFeed reaches recordSuccess.
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Old Title", SiteURL: "https://old.test"}}
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("x"), ETag: `"oldtag"`}}
	svc, clk := newFeedSvc(store, fetcher, parser)

	id, err := store.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://old.test/f", Title: "Old Title",
		NextCheckAt: clk.Now(), CreatedAt: clk.Now(), UpdatedAt: clk.Now(),
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	// Poll dispatched with the OLD URL snapshot.
	snapshot, _ := store.GetFeed(ctx, core.DefaultUserID, id)

	// The user edits the URL mid-poll (clears validators, resets schedule).
	if err := store.SetFeedURL(ctx, core.DefaultUserID, id, "https://new.test/f", clk.Now()); err != nil {
		t.Fatalf("SetFeedURL: %v", err)
	}

	// The stale poll completes and tries to write back the old URL's metadata.
	if err := svc.PollFeed(ctx, snapshot); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	got, _ := store.GetFeed(ctx, core.DefaultUserID, id)
	if got.FeedURL != "https://new.test/f" {
		t.Fatalf("feed_url = %q, want the edited URL", got.FeedURL)
	}
	if got.ETag != "" {
		t.Errorf("stale poll resurrected the old URL's etag: %q", got.ETag)
	}
}
