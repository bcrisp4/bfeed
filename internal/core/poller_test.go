package core_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

type countingPoller struct {
	n    int32
	seen sync.Map
}

func (p *countingPoller) PollFeed(_ context.Context, f *core.Feed) error {
	atomic.AddInt32(&p.n, 1)
	p.seen.Store(f.ID, true)
	// Push next check into the future so it isn't re-dispatched every tick.
	f.NextCheckAt = f.NextCheckAt.Add(time.Hour)
	return nil
}

func TestPollerDispatchesDueFeeds(t *testing.T) {
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://h.test/" + string(rune('a'+i)), NextCheckAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now})
	}
	cp := &countingPoller{}
	clk := coretest.StubClock{T: now}
	p := core.NewPoller(store, cp, clk, coretest.DiscardLogger(), core.PollerConfig{Tick: 5 * time.Millisecond, BatchSize: 10, Workers: 2})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { p.Run(runCtx); close(done) }()

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&cp.n) < 3 {
		select {
		case <-deadline:
			t.Fatalf("poller only polled %d/3 feeds", atomic.LoadInt32(&cp.n))
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
