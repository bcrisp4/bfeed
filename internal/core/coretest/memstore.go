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
	settings    map[string]string
	nextID      core.ID
}

func NewMemStore() *MemStore {
	return &MemStore{
		feeds:       map[core.ID]*core.Feed{},
		entries:     map[core.ID]*core.Entry{},
		tombstones:  map[string]bool{},
		categories:  map[core.ID]*core.Category{},
		nextExtract: map[core.ID]time.Time{},
		settings:    map[string]string{},
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
	// Mirror the real query's `ORDER BY title COLLATE NOCASE ASC` so web tests that
	// range ListFeeds into group rows get deterministic order (same as ListCategories).
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
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

// WeeklyEntryCount mirrors the sqlite query: count a feed's entries in
// [now-week, now], using published_at when present (Unix > 0) and falling back
// to ingest time (created_at) when the publisher omitted a date.
func (s *MemStore) WeeklyEntryCount(_ context.Context, feedID core.ID, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := now.Add(-core.Week)
	n := 0
	for _, e := range s.entries {
		if e.FeedID != feedID {
			continue
		}
		eff := e.PublishedAt
		if eff.Unix() <= 0 { // no/zero published date -> ingest time
			eff = e.CreatedAt
		}
		if !eff.Before(start) && !eff.After(now) {
			n++
		}
	}
	return n, nil
}

// UpdateFeed updates only the poll-owned fields, matching the real SQLite
// UpdateFeed query (which does not touch FetchFullContent, UserTitle, CategoryID,
// or FeedURL — those are user-owned and updated by dedicated store methods).
func (s *MemStore) UpdateFeed(_ context.Context, f *core.Feed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.feeds[f.ID]
	if !ok || ex.UserID != f.UserID || ex.FeedURL != f.FeedURL {
		// The real UpdateFeed is `:exec ... WHERE id = ? AND user_id = ? AND feed_url = ?`,
		// so a missing/other-user feed (poll-races-a-delete) OR a feed whose URL a
		// concurrent edit changed mid-poll (the feed_url CAS guard) is a silent no-op
		// in production, not an error (audit B7).
		return nil
	}
	ex.SiteURL = f.SiteURL
	ex.Title = f.Title
	ex.Description = f.Description
	ex.ETag = f.ETag
	ex.LastModified = f.LastModified
	ex.Disabled = f.Disabled
	ex.CheckedAt = f.CheckedAt
	ex.NextCheckAt = f.NextCheckAt
	ex.ErrorCount = f.ErrorCount
	ex.LastError = f.LastError
	ex.UpdatedAt = f.UpdatedAt
	ex.TTL = f.TTL
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
			delete(s.nextExtract, eid) // mirror the cascade; don't leak schedule state
		}
	}
	// Mirror the FK ON DELETE CASCADE on tombstones.feed_id: dropping the feed drops
	// its tombstones too (a re-subscribe gets a fresh feed_id and a clean slate).
	prefix := fmt.Sprintf("%d|", id)
	for k := range s.tombstones {
		if strings.HasPrefix(k, prefix) {
			delete(s.tombstones, k)
		}
	}
	delete(s.feeds, id)
	return nil
}

func (s *MemStore) EntryDedup(_ context.Context, feedID core.ID, guids []string) (map[string]string, map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[string]bool, len(guids))
	for _, g := range guids {
		want[g] = true
	}
	existing := make(map[string]string)
	for _, e := range s.entries {
		if e.FeedID == feedID && want[e.GUID] {
			existing[e.GUID] = e.Hash
		}
	}
	tombstoned := make(map[string]bool)
	for _, g := range guids {
		if s.tombstones[tkey(feedID, g)] {
			tombstoned[g] = true
		}
	}
	return existing, tombstoned, nil
}

func (s *MemStore) UpsertEntries(_ context.Context, feedID core.ID, es []*core.Entry) ([]*core.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Mirror store/sqlite: decide extract_state from the feed's CURRENT
	// FetchFullContent read inside the "transaction", not the caller's snapshot
	// (audit B7). A gone feed (never id-reused, since feeds are AUTOINCREMENT) ingests
	// nothing — the real store's FK would reject the insert. (SeedEntry stages entries
	// via seedEntry, not this path, so it is unaffected.)
	feed, ok := s.feeds[feedID]
	if !ok {
		return nil, nil
	}
	insertState := core.ExtractNone
	if feed.FetchFullContent {
		insertState = core.ExtractPending
	}
	var ins []*core.Entry
	for _, e := range es {
		if s.tombstones[tkey(feedID, e.GUID)] {
			continue
		}
		var existing *core.Entry
		for _, ex := range s.entries {
			if ex.FeedID == feedID && ex.GUID == e.GUID {
				existing = ex
				break
			}
		}
		if existing != nil {
			// Mirror store/sqlite: re-fetched entries upsert by content hash — when
			// the hash changed, overwrite the poll-owned content fields in place and
			// leave user state (Status/Starred/ReadAt) untouched. Not an "insert".
			if existing.Hash != e.Hash {
				existing.Title = e.Title
				existing.Author = e.Author
				existing.Content = e.Content
				existing.Summary = e.Summary
				existing.PublishedAt = e.PublishedAt
				existing.URL = e.URL
				existing.Hash = e.Hash
				existing.CommentsURL = e.CommentsURL
			}
			continue
		}
		id := s.nextID
		s.nextID++
		cp := *e
		cp.ID = id
		cp.FeedID = feedID
		cp.ExtractState = insertState
		if cp.ExtractState == core.ExtractPending {
			s.nextExtract[id] = cp.CreatedAt
		}
		s.entries[id] = &cp
		ins = append(ins, &cp)
	}
	return ins, nil
}

// SeedEntry stages e verbatim (honoring its ExtractState), bypassing UpsertEntries'
// feed-derived extract_state logic. Test-only helper for staging arbitrary entry
// state; see coretest.SeedEntry.
func (s *MemStore) SeedEntry(e *core.Entry) core.ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	cp := *e
	cp.ID = id
	if cp.ExtractState == "" {
		cp.ExtractState = core.ExtractNone
	}
	if cp.ExtractState == core.ExtractPending {
		s.nextExtract[id] = cp.CreatedAt
	}
	s.entries[id] = &cp
	return id
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
		out = append(out, previewEntry(e))
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := core.CursorKey(out[i], f.Order), core.CursorKey(out[j], f.Order)
		if ki != kj {
			return ki > kj
		}
		return out[i].ID > out[j].ID
	})
	if f.Cursor != nil {
		var after []*core.Entry
		for _, e := range out {
			k := core.CursorKey(e, f.Order)
			if k < f.Cursor.Key || (k == f.Cursor.Key && int64(e.ID) < int64(f.Cursor.ID)) {
				after = append(after, e)
			}
		}
		out = after
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 { // mirror store/sqlite ListEntries clamp
		limit = 50
	}
	var next *core.Cursor
	if len(out) > limit {
		last := out[limit-1]
		next = &core.Cursor{Key: core.CursorKey(last, f.Order), ID: last.ID}
		out = out[:limit]
	}
	return out, next, nil
}

// previewEntry copies an entry with Content/Summary truncated to the list-preview
// length, mirroring the store's substr(content,1,2048) projection. Rune-based (not
// a byte slice) so the fake matches SQLite substr's character semantics — a byte
// slice would split a multibyte rune the real store never splits.
func previewEntry(e *core.Entry) *core.Entry {
	cp := *e
	cp.Content = truncRunes(cp.Content, 2048)
	cp.Summary = truncRunes(cp.Summary, 2048)
	return &cp
}

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
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

func (s *MemStore) MarkReadByFilter(_ context.Context, u core.ID, f core.EntryFilter) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	n := 0
	for _, e := range s.entries {
		if e.UserID != u || e.Status != core.StatusUnread {
			continue
		}
		// Selection precedence mirrors the sqlite MarkReadByFilter switch
		// (FeedID, else CategoryID, else Uncategorised) — exclusive, not
		// AND-combined, so the fake doesn't lie about combined filters.
		switch {
		case f.FeedID != nil:
			if e.FeedID != *f.FeedID {
				continue
			}
		case f.CategoryID != nil:
			fd, ok := s.feeds[e.FeedID]
			if !ok || fd.CategoryID == nil || *fd.CategoryID != *f.CategoryID {
				continue
			}
		case f.Uncategorised:
			fd, ok := s.feeds[e.FeedID]
			if !ok || fd.CategoryID != nil {
				continue
			}
		}
		e.Status = core.StatusRead
		rt := now
		e.ReadAt = &rt
		n++
	}
	return n, nil
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

// SetFeedFullContent flips the flag and reconciles the backlog atomically (one
// lock hold), mirroring the sqlite transaction: enable backfills none/failed
// entries to pending (resetting attempts + clearing the reason, per #6), disable
// cancels queued ones.
func (s *MemStore) SetFeedFullContent(_ context.Context, u, feedID core.ID, on bool, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[feedID]
	if !ok || f.UserID != u {
		return core.ErrNotFound
	}
	f.FetchFullContent = on
	for _, e := range s.entries {
		if e.FeedID != feedID {
			continue
		}
		if on {
			if e.ExtractState == core.ExtractNone || e.ExtractState == core.ExtractFailed {
				e.ExtractState = core.ExtractPending
				e.ExtractAttempts = 0
				e.ExtractError = ""
				s.nextExtract[e.ID] = at
			}
		} else if e.ExtractState == core.ExtractPending || e.ExtractState == core.ExtractFailed {
			e.ExtractState = core.ExtractNone
			e.ExtractError = ""
			delete(s.nextExtract, e.ID)
		}
	}
	return nil
}

func (s *MemStore) SetFeedUserTitle(_ context.Context, u, feedID core.ID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[feedID]
	if !ok || f.UserID != u {
		return core.ErrNotFound
	}
	cp := *f
	cp.UserTitle = title
	s.feeds[feedID] = &cp
	return nil
}

func (s *MemStore) SetFeedURL(_ context.Context, u, feedID core.ID, url string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.feeds[feedID]
	if !ok || f.UserID != u {
		return core.ErrNotFound
	}
	for _, other := range s.feeds { // mirror UNIQUE(user_id, feed_url) in the real store
		if other.UserID == u && other.FeedURL == url && other.ID != feedID {
			return core.ErrConflict
		}
	}
	cp := *f
	cp.FeedURL = url
	cp.ETag = ""         // mirror the SQL: stale conditional-GET headers must not
	cp.LastModified = "" // carry across a URL change (would risk a spurious 304).
	cp.NextCheckAt = now // make the new URL promptly poll-due (audit B7).
	s.feeds[feedID] = &cp
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
		// Match visible text only, mirroring the store's plain-text FTS projection
		// (a query for "https" or "href" must not match markup). See core.PlainText.
		hay := strings.ToLower(e.Title + " " + core.PlainText(e.Content) + " " + core.PlainText(e.Summary))
		match := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				match = false
				break
			}
		}
		if match {
			out = append(out, previewEntry(e))
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
	// Mirror the SQL CAS `WHERE id = ? AND extract_state = 'pending'`: a stale scraper
	// writing onto a recycled/already-settled entry id is a silent no-op (audit B7).
	if e.ExtractState != core.ExtractPending {
		return nil
	}
	e.Content = content
	e.ExtractState = core.ExtractDone
	e.ExtractError = "" // mirror SQL: a successful extract clears the failure reason
	delete(s.nextExtract, entryID)
	return nil
}

func (s *MemStore) UpdateExtractState(_ context.Context, entryID core.ID, state core.ExtractState, attempts int, nextAt *time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[entryID]
	if !ok {
		return core.ErrNotFound
	}
	e.ExtractState = state
	e.ExtractAttempts = attempts
	e.ExtractError = reason
	if nextAt != nil {
		s.nextExtract[entryID] = *nextAt
	} else {
		delete(s.nextExtract, entryID)
	}
	return nil
}

func (s *MemStore) EntryStatsByFeed(_ context.Context, u core.ID) (map[core.ID]core.FeedEntryStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[core.ID]core.FeedEntryStats{}
	for _, e := range s.entries {
		if e.UserID != u {
			continue
		}
		st := out[e.FeedID]
		st.Total++
		if e.Status == core.StatusUnread {
			st.Unread++
		}
		out[e.FeedID] = st
	}
	return out, nil
}

func (s *MemStore) UnreadCount(_ context.Context, u core.ID) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.entries {
		if e.UserID == u && e.Status == core.StatusUnread {
			n++
		}
	}
	return n, nil
}

func (s *MemStore) FeedEntryStatsByID(_ context.Context, u, feedID core.ID) (core.FeedEntryStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st core.FeedEntryStats
	for _, e := range s.entries {
		if e.UserID != u || e.FeedID != feedID {
			continue
		}
		st.Total++
		if e.Status == core.StatusUnread {
			st.Unread++
		}
	}
	return st, nil
}

func (s *MemStore) GetSetting(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	if !ok {
		return "", core.ErrNotFound
	}
	return v, nil
}

func (s *MemStore) PutSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
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
