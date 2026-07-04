package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

// errDueLister always fails ListDueFeeds, simulating a store-layer failure at
// dispatch time.
type errDueLister struct{}

func (errDueLister) ListDueFeeds(context.Context, time.Time, int) ([]*core.Feed, error) {
	return nil, errors.New("boom")
}

// TestPollerTickedEmittedEvenOnStoreError proves liveness (PollerTicked) does
// not stall on a ListDueFeeds failure — the tick must be recorded before the
// store call, and the failure itself must surface as ErrorObserved(feed_poll,
// internal).
func TestPollerTickedEmittedEvenOnStoreError(t *testing.T) {
	clk := coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	p := core.NewPoller(errDueLister{}, noopPoller{}, clk, coretest.DiscardLogger(), core.PollerConfig{Tick: time.Hour, BatchSize: 10, Workers: 1})
	m := &coretest.RecordingMetrics{}
	p.SetMetrics(m)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for len(m.SnapshotPollerTicks()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	ticks := m.SnapshotPollerTicks()
	if len(ticks) != 1 {
		t.Fatalf("PollerTicked called %d times, want exactly 1 (initial dispatch)", len(ticks))
	}
	if !ticks[0].Equal(clk.T) {
		t.Fatalf("PollerTicked time = %v, want %v (from clk.Now())", ticks[0], clk.T)
	}

	errs := m.SnapshotErrors()
	want := coretest.RecordedError{C: core.CompFeedPoll, R: core.ReasonInternal}
	if len(errs) != 1 || errs[0] != want {
		t.Fatalf("ErrorObserved = %v, want exactly [%v]", errs, want)
	}
}

// noopPoller is a FeedPoller that is never expected to be invoked in the
// store-error test above (dispatch returns before any job is queued).
type noopPoller struct{}

func (noopPoller) PollFeed(context.Context, *core.Feed) error { return nil }

// TestPollerInflightTracksBlockingPoll asserts AddPollInflight(1) is emitted
// before a poll starts and AddPollInflight(-1) after it finishes (via the
// defer ordered after RecoverGuard, so a panicking poll would still
// decrement) — proven here with a poll that blocks on a channel rather than
// panicking, since blocking lets the test observe the inflight count while
// the poll is still in progress.
func TestPollerInflightTracksBlockingPoll(t *testing.T) {
	store := coretest.NewMemStore()
	now := time.Unix(1_700_000_000, 0).UTC()
	ctx := context.Background()
	if _, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://h.test/a", NextCheckAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	bp := &blockingPoller{started: started, release: release}
	clk := coretest.StubClock{T: now}
	p := core.NewPoller(store, bp, clk, coretest.DiscardLogger(), core.PollerConfig{Tick: time.Hour, BatchSize: 10, Workers: 1})
	m := &coretest.RecordingMetrics{}
	p.SetMetrics(m)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { p.Run(runCtx); close(done) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("poll never started")
	}

	deadline := time.Now().Add(2 * time.Second)
	for m.PollInflight() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.PollInflight(); got != 1 {
		t.Fatalf("PollInflight = %d while poll blocked, want 1", got)
	}

	close(release)

	deadline = time.Now().Add(2 * time.Second)
	for m.PollInflight() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.PollInflight(); got != 0 {
		t.Fatalf("PollInflight = %d after poll released, want 0", got)
	}

	cancel()
	<-done
}

// blockingPoller signals started (once) then blocks until release closes.
type blockingPoller struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (p *blockingPoller) PollFeed(ctx context.Context, _ *core.Feed) error {
	close(p.started)
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	return nil
}
