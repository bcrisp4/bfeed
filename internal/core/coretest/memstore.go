// Package coretest provides in-memory test doubles for core's ports, shared by
// core service tests and web handler tests. It is a normal (non-_test) package
// so it can be imported across package boundaries; consumers are external test
// packages (e.g. package core_test), avoiding any import cycle.
package coretest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// MemStore is an in-memory core.Store.
type MemStore struct {
	mu         sync.Mutex
	feeds      map[core.ID]*core.Feed
	entries    map[core.ID]*core.Entry
	tombstones map[string]bool // feedID|guid
	nextID     core.ID
}

func NewMemStore() *MemStore {
	return &MemStore{feeds: map[core.ID]*core.Feed{}, entries: map[core.ID]*core.Entry{}, tombstones: map[string]bool{}, nextID: 1}
}

var _ core.Store = (*MemStore)(nil)

func tkey(f core.ID, g string) string { return fmt.Sprintf("%d|%s", f, g) }

func (s *MemStore) CreateFeed(_ context.Context, f *core.Feed) (core.ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.feeds {
		if ex.UserID == f.UserID && ex.FeedURL == f.FeedURL {
			return 0, core.ErrConflict
		}
	}
	id := s.nextID
	s.nextID++
	cp := *f
	cp.ID = id
	s.feeds[id] = &cp
	return id, nil
}

func (s *MemStore) GetFeed(_ context.Context, u, id core.ID) (*core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[id]
	if !ok || f.UserID != u {
		return nil, core.ErrNotFound
	}
	cp := *f
	return &cp, nil
}

func (s *MemStore) ListFeeds(_ context.Context, u core.ID) ([]*core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.Feed
	for _, f := range s.feeds {
		if f.UserID == u {
			cp := *f
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *MemStore) ListDueFeeds(_ context.Context, now time.Time, limit int) ([]*core.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.Feed
	for _, f := range s.feeds {
		if !f.Disabled && !f.NextCheckAt.After(now) {
			cp := *f
			out = append(out, &cp)
		}
	}
	// Order is unspecified in this test double; truncate to honor limit.
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemStore) UpdateFeed(_ context.Context, f *core.Feed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.feeds[f.ID]; !ok {
		return core.ErrNotFound
	}
	cp := *f
	s.feeds[f.ID] = &cp
	return nil
}

func (s *MemStore) DeleteFeed(_ context.Context, u, id core.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[id]
	if !ok || f.UserID != u {
		return core.ErrNotFound
	}
	for eid, e := range s.entries {
		if e.FeedID == id {
			delete(s.entries, eid)
		}
	}
	delete(s.feeds, id)
	return nil
}

func (s *MemStore) UpsertEntries(_ context.Context, feedID core.ID, es []*core.Entry) ([]*core.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ins []*core.Entry
	for _, e := range es {
		if s.tombstones[tkey(feedID, e.GUID)] {
			continue
		}
		dup := false
		for _, ex := range s.entries {
			if ex.FeedID == feedID && ex.GUID == e.GUID {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		id := s.nextID
		s.nextID++
		cp := *e
		cp.ID = id
		cp.FeedID = feedID
		s.entries[id] = &cp
		ins = append(ins, &cp)
	}
	return ins, nil
}

func (s *MemStore) GetEntry(_ context.Context, u, id core.ID) (*core.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || e.UserID != u {
		return nil, core.ErrNotFound
	}
	cp := *e
	return &cp, nil
}

func (s *MemStore) ListEntries(_ context.Context, u core.ID, f core.EntryFilter) ([]*core.Entry, *core.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.Entry
	for _, e := range s.entries {
		if e.UserID != u {
			continue
		}
		if f.Status != nil && e.Status != *f.Status {
			continue
		}
		if f.FeedID != nil && e.FeedID != *f.FeedID {
			continue
		}
		if f.Starred != nil && e.Starred != *f.Starred {
			continue
		}
		if f.Order == core.OrderReadAtDesc && e.ReadAt == nil { // history membership
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := memSortKey(out[i], f.Order), memSortKey(out[j], f.Order)
		if ki != kj {
			return ki > kj
		}
		return out[i].ID > out[j].ID
	})
	if f.Cursor != nil {
		var after []*core.Entry
		for _, e := range out {
			k := memSortKey(e, f.Order)
			if k < f.Cursor.Key || (k == f.Cursor.Key && int64(e.ID) < int64(f.Cursor.ID)) {
				after = append(after, e)
			}
		}
		out = after
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	var next *core.Cursor
	if len(out) > limit {
		last := out[limit-1]
		next = &core.Cursor{Key: memSortKey(last, f.Order), ID: last.ID}
		out = out[:limit]
	}
	return out, next, nil
}

// memSortKey returns the unix-seconds value of the entry's active order column.
func memSortKey(e *core.Entry, ord core.Order) int64 {
	if ord == core.OrderReadAtDesc && e.ReadAt != nil {
		return e.ReadAt.Unix()
	}
	return e.PublishedAt.Unix()
}

func (s *MemStore) SetStatus(_ context.Context, u core.ID, ids []core.ID, st core.EntryStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if e, ok := s.entries[id]; ok && e.UserID == u {
			e.Status = st
			if st == core.StatusRead {
				now := time.Now().UTC()
				e.ReadAt = &now
			} else {
				e.ReadAt = nil
			}
		}
	}
	return nil
}

func (s *MemStore) SetStarred(_ context.Context, u core.ID, ids []core.ID, v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if e, ok := s.entries[id]; ok && e.UserID == u {
			e.Starred = v
		}
	}
	return nil
}

func (s *MemStore) DeleteEntry(_ context.Context, u, id core.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || e.UserID != u {
		return core.ErrNotFound
	}
	s.tombstones[tkey(e.FeedID, e.GUID)] = true
	delete(s.entries, id)
	return nil
}
