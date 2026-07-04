package web_test

import (
	"context"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/web"
)

// newDrainHandler builds a web.Handler (concrete type, so Drain is reachable)
// wired with the given fetcher.
func newDrainHandler(t *testing.T, fetcher core.Fetcher) (*web.Handler, *coretest.MemStore) {
	t.Helper()
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, fetcher, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	return web.New(fs, es, cs, ss, log, nil, nil, 20, "", nil, nil), store
}

// Drain must block until an in-flight background feed op (subscribe/refresh
// goroutine) completes, so graceful shutdown does not close the DB underneath a
// mid-flight write.
func TestDrainWaitsForInFlightBackgroundOps(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h, st := newDrainHandler(t, coretest.BlockingFetcher(started, release))
	id := seedFeed(t, st)

	// Kick off a background refresh that blocks in the fetcher.
	do(t, h, "POST", "/feeds/"+itoa(id)+"/refresh")
	<-started // background goroutine reached the fetcher

	// Drain must not return while the op is in flight.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if h.Drain(ctx) {
		t.Fatal("Drain returned true while a background op was still in flight")
	}

	close(release) // let the op finish
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if !h.Drain(ctx2) {
		t.Fatal("Drain did not complete after the background op finished")
	}
}

// Drain returns immediately when nothing is in flight.
func TestDrainReturnsWhenIdle(t *testing.T) {
	h, _ := newDrainHandler(t, coretest.StubFetcher{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !h.Drain(ctx) {
		t.Fatal("Drain should return true immediately when idle")
	}
}
