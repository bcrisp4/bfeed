package web

import (
	"sync"

	"github.com/bcrisp4/bfeed/internal/core"
)

// inflightSet tracks feed ids with a background subscribe/refresh in progress.
// It is web-layer-only, in-memory state (single process, single user): the feed
// rows render from the DB, which is the source of truth; this set only tells a
// row fragment whether to keep polling. A process restart simply stops any
// in-progress spinners.
type inflightSet struct {
	mu  sync.Mutex
	ids map[core.ID]struct{}
}

func newInflightSet() *inflightSet {
	return &inflightSet{ids: map[core.ID]struct{}{}}
}

// start marks id in flight, returning false if it already was (caller should
// then not spawn a duplicate goroutine).
func (s *inflightSet) start(id core.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false
	}
	s.ids[id] = struct{}{}
	return true
}

func (s *inflightSet) done(id core.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ids, id)
}

func (s *inflightSet) has(id core.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}
