package clock

import (
	"sync"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// Real is the production clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Fake is a deterministic clock for tests.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

var (
	_ core.Clock = Real{}
	_ core.Clock = (*Fake)(nil)
)
