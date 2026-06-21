package core

import (
	"context"
	"log/slog"
)

type EntryService struct {
	store EntryStore
	log   *slog.Logger
}

func NewEntryService(store EntryStore, log *slog.Logger) *EntryService {
	return &EntryService{store: store, log: log}
}

func (s *EntryService) List(ctx context.Context, userID ID, f EntryFilter) ([]*Entry, *Cursor, error) {
	return s.store.ListEntries(ctx, userID, f)
}

func (s *EntryService) Get(ctx context.Context, userID, entryID ID) (*Entry, error) {
	return s.store.GetEntry(ctx, userID, entryID)
}

func (s *EntryService) MarkRead(ctx context.Context, userID ID, ids []ID, read bool) error {
	st := StatusUnread
	if read {
		st = StatusRead
	}
	return s.store.SetStatus(ctx, userID, ids, st)
}

// MarkAllRead marks every unread entry matching f's selection (feed / category /
// uncategorised; empty = all the user's feeds) read. Returns the number affected.
func (s *EntryService) MarkAllRead(ctx context.Context, userID ID, f EntryFilter) (int, error) {
	return s.store.MarkReadByFilter(ctx, userID, f)
}

func (s *EntryService) Star(ctx context.Context, userID ID, ids []ID, starred bool) error {
	return s.store.SetStarred(ctx, userID, ids, starred)
}

func (s *EntryService) Delete(ctx context.Context, userID, entryID ID) error {
	return s.store.DeleteEntry(ctx, userID, entryID)
}
