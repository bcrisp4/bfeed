package core

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type PollerConfig struct {
	Tick      time.Duration
	BatchSize int
	Workers   int
}

type Poller struct {
	store FeedStore
	poll  FeedPoller
	clk   Clock
	log   *slog.Logger
	cfg   PollerConfig
}

func NewPoller(store FeedStore, poll FeedPoller, clk Clock, log *slog.Logger, cfg PollerConfig) *Poller {
	if cfg.Workers <= 0 {
		cfg.Workers = 20
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.Tick <= 0 {
		cfg.Tick = time.Minute
	}
	return &Poller{store: store, poll: poll, clk: clk, log: log, cfg: cfg}
}

// Run blocks until ctx is cancelled, dispatching due feeds each tick.
func (p *Poller) Run(ctx context.Context) {
	jobs := make(chan *Feed)
	var wg sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				if err := p.poll.PollFeed(ctx, f); err != nil {
					p.log.Error("poll feed", "feed_id", int64(f.ID), "error", err)
				}
			}
		}()
	}

	ticker := time.NewTicker(p.cfg.Tick)
	defer ticker.Stop()
	p.dispatch(ctx, jobs)
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case <-ticker.C:
			p.dispatch(ctx, jobs)
		}
	}
}

func (p *Poller) dispatch(ctx context.Context, jobs chan<- *Feed) {
	due, err := p.store.ListDueFeeds(ctx, p.clk.Now(), p.cfg.BatchSize)
	if err != nil {
		p.log.Error("list due feeds", "error", err)
		return
	}
	for _, f := range due {
		select {
		case <-ctx.Done():
			return
		case jobs <- f:
		}
	}
}
