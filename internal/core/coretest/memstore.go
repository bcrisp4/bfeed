// Package coretest provides in-memory test doubles for core's ports, shared by
// core service tests and web handler tests. It is a normal (non-_test) package
// so it can be imported across package boundaries; consumers are external test
// packages (e.g. package core_test), avoiding any import cycle.
package coretest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

// MemStore is an in-memory core.Store.
type MemStore struct {
	mu          sync.Mutex
	feeds       map[core.ID]*core.Feed
	entries     map[core.ID]*core.Entry
	tombstones  map[string]bool // feedID|guid
	categories  map[core.ID]*core.Category
	nextExtract map[core.ID]time.Time // per-entry next extraction time
	nextID      core.ID
}

func NewMemStore() *MemStore {
	return &MemStore{
		feeds:       map[core.ID]*core.Feed{},
		entries:     map[core.ID]*core.Entry{},
		tombstones:  map[string]bool{},
		categories:  map[core.ID]*core.Category{},
		nextExtract: map[core.ID]time.Time{},
		nextID:      1,
	}
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
		if cp.ExtractState == "" {
			cp.ExtractState = core.ExtractNone
		}
		if cp.ExtractState == core.ExtractPending {
			s.nextExtract[id] = cp.CreatedAt
		}
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
		if f.CategoryID != nil || f.Uncategorised {
			fd, ok := s.feeds[e.FeedID]
			if !ok {
				continue
			}
			if f.Uncategorised && fd.CategoryID != nil {
				continue
			}
			if f.CategoryID != nil && (fd.CategoryID == nil || *fd.CategoryID != *f.CategoryID) {
				continue
			}
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

func (s *MemStore) CreateCategory(_ context.Context, c *core.Category) (core.ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.categories {
		if ex.UserID == c.UserID && ex.Title == c.Title { // UNIQUE(user_id,title), case-sensitive
			return 0, core.ErrConflict
		}
	}
	id := s.nextID
	s.nextID++
	cp := *c
	cp.ID = id
	s.categories[id] = &cp
	return id, nil
}

func (s *MemStore) GetCategory(_ context.Context, u, id core.ID) (*core.Category, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.categories[id]
	if !ok || c.UserID != u {
		return nil, core.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *MemStore) ListCategories(_ context.Context, u core.ID) ([]*core.Category, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.Category
	for _, c := range s.categories {
		if c.UserID == u {
			cp := *c
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

func (s *MemStore) UpdateCategory(_ context.Context, c *core.Category) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.categories[c.ID]
	if !ok || ex.UserID != c.UserID {
		return core.ErrNotFound
	}
	for _, other := range s.categories {
		if other.UserID == c.UserID && other.Title == c.Title && other.ID != c.ID {
			return core.ErrConflict
		}
	}
	cp := *ex
	cp.Title = c.Title
	s.categories[c.ID] = &cp
	return nil
}

func (s *MemStore) DeleteCategory(_ context.Context, u, id core.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.categories[id]
	if !ok || c.UserID != u {
		return core.ErrNotFound
	}
	// Mirror ON DELETE SET NULL: re-home feeds to uncategorised.
	for _, f := range s.feeds {
		if f.CategoryID != nil && *f.CategoryID == id {
			f.CategoryID = nil
		}
	}
	delete(s.categories, id)
	return nil
}

func (s *MemStore) SetFeedFullContent(_ context.Context, u, feedID core.ID, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[feedID]
	if !ok || f.UserID != u {
		return core.ErrNotFound
	}
	f.FetchFullContent = on
	return nil
}

func (s *MemStore) SetFeedCategory(_ context.Context, u, feedID core.ID, categoryID *core.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[feedID]
	if !ok || f.UserID != u {
		return core.ErrNotFound
	}
	if categoryID == nil {
		f.CategoryID = nil
	} else {
		cp := *categoryID
		f.CategoryID = &cp
	}
	return nil
}

// Search is a behavioral fake of core.SearchIndex: case-insensitive AND
// substring match over title+content+summary, newest-first, capped at 50.
// It is not bm25 — tests must not assert relevance order against it.
func (s *MemStore) Search(_ context.Context, u core.ID, query string, _ core.EntryFilter) ([]*core.Entry, *core.Cursor, error) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.Entry
	for _, e := range s.entries {
		if e.UserID != u {
			continue
		}
		hay := strings.ToLower(e.Title + " " + e.Content + " " + e.Summary)
		match := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				match = false
				break
			}
		}
		if match {
			cp := *e
			out = append(out, &cp)
		}
	}
	// Published-desc with an id-desc tiebreak, matching ListEntries so equal
	// timestamps order deterministically (the fake must not lie about ordering).
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PublishedAt.Equal(out[j].PublishedAt) {
			return out[i].PublishedAt.After(out[j].PublishedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil, nil
}

func (s *MemStore) ListPendingExtractions(_ context.Context, now time.Time, limit int) ([]*core.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*core.Entry
	for _, e := range s.entries {
		if e.ExtractState != core.ExtractPending {
			continue
		}
		if t, ok := s.nextExtract[e.ID]; ok && t.After(now) {
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PublishedAt.Equal(out[j].PublishedAt) {
			return out[i].PublishedAt.After(out[j].PublishedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemStore) SetEntryContent(_ context.Context, entryID core.ID, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok {
		return core.ErrNotFound
	}
	e.Content = content
	e.ExtractState = core.ExtractDone
	delete(s.nextExtract, entryID)
	return nil
}

func (s *MemStore) UpdateExtractState(_ context.Context, entryID core.ID, state core.ExtractState, attempts int, nextAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok {
		return core.ErrNotFound
	}
	e.ExtractState = state
	e.ExtractAttempts = attempts
	if nextAt != nil {
		s.nextExtract[entryID] = *nextAt
	} else {
		delete(s.nextExtract, entryID)
	}
	return nil
}

func (s *MemStore) MarkFeedEntriesPending(_ context.Context, feedID core.ID, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.FeedID == feedID && (e.ExtractState == core.ExtractNone || e.ExtractState == core.ExtractFailed) {
			e.ExtractState = core.ExtractPending
			s.nextExtract[e.ID] = at
		}
	}
	return nil
}

func (s *MemStore) CancelFeedExtractions(_ context.Context, feedID core.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.FeedID == feedID && e.ExtractState == core.ExtractPending {
			e.ExtractState = core.ExtractNone
			delete(s.nextExtract, e.ID)
		}
	}
	return nil
}

func (s *MemStore) UnreadCountsByCategory(_ context.Context, u core.ID) (map[core.ID]int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	perCat := map[core.ID]int{}
	uncat := 0
	for _, e := range s.entries {
		if e.UserID != u || e.Status != core.StatusUnread {
			continue
		}
		f, ok := s.feeds[e.FeedID]
		if !ok {
			continue
		}
		if f.CategoryID == nil {
			uncat++
		} else {
			perCat[*f.CategoryID]++
		}
	}
	return perCat, uncat, nil
}
