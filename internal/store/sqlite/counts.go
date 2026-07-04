package sqlite

import (
	"context"
	"time"
)

// Global, system-level counts consumed by the Prometheus stats collector
// (no user_id scoping -- deliberate, per the documented background-sweep
// exception: these mirror the dispatch queries' own unscoped semantics).

// CountUsers returns the total number of users.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	n, err := s.q.CountUsers(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// CountFeeds returns the total number of feeds across all users.
func (s *Store) CountFeeds(ctx context.Context) (int64, error) {
	n, err := s.q.CountFeeds(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// CountEntries returns the total number of entries across all users.
func (s *Store) CountEntries(ctx context.Context) (int64, error) {
	n, err := s.q.CountEntries(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// CountDueFeeds returns the number of feeds ListDueFeeds would dispatch on a
// tick at `now` (ignoring LIMIT) -- i.e. the poll backlog.
func (s *Store) CountDueFeeds(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.q.CountDueFeeds(ctx, toUnix(now))
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}

// CountDueExtractions returns the number of entries ListPendingExtractions
// would dispatch on a tick at `now` (ignoring LIMIT) -- i.e. the scrape
// backlog.
func (s *Store) CountDueExtractions(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.q.CountDueExtractions(ctx, nullUnix(&now))
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}
