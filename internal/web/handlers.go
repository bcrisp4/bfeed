package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/bcrisp4/bfeed/internal/core"
)

const uid = core.DefaultUserID

type entryVM struct {
	ID        core.ID
	Title     string
	URL       string
	Author    string
	Content   templateHTML
	Status    string
	Starred   bool
	FeedID    core.ID
	FeedTitle string
	Published string
}

type listVM struct {
	Title      string
	ListPath   string
	Entries    []entryVM
	NextCursor string
}

func (h *Handler) unread(w http.ResponseWriter, r *http.Request) {
	st := core.StatusUnread
	h.renderList(w, r, "Unread", "/", core.EntryFilter{Status: &st})
}

func (h *Handler) starred(w http.ResponseWriter, r *http.Request) {
	star := true
	h.renderList(w, r, "Starred", "/starred", core.EntryFilter{Starred: &star})
}

func (h *Handler) feedEntries(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	h.renderList(w, r, "Feed", "/feeds/"+strconv.FormatInt(int64(id), 10), core.EntryFilter{FeedID: &id})
}

// renderList fetches feeds once, builds a feed-title map, then lists entries.
// This avoids the O(entries×feeds) cost of the per-entry lookup the brief describes.
func (h *Handler) renderList(w http.ResponseWriter, r *http.Request, title, path string, f core.EntryFilter) {
	if c := r.URL.Query().Get("cursor"); c != "" {
		f.Cursor = core.DecodeCursor(c)
	}
	f.Limit = 50

	// Fetch feeds once and build a map for O(1) title lookup per entry.
	feedTitles := h.feedTitleMap(r.Context())

	entries, next, err := h.entries.List(r.Context(), uid, f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	vm := listVM{Title: title, ListPath: path, Entries: toEntryVMs(entries, feedTitles)}
	if next != nil {
		vm.NextCursor = core.EncodeCursor(*next)
	}
	// htmx "load more" requests only the rows; full-page requests get the full layout.
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("cursor") != "" {
		if err := h.tmpl["entries"].ExecuteTemplate(w, "entrylist", vm); err != nil {
			h.log.Error("template execute", "template", "entries/entrylist", "error", err)
		}
		return
	}
	if err := h.tmpl["entries"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "entries/layout", "error", err)
	}
}

func (h *Handler) entry(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	e, err := h.entries.Get(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Mark read on open.
	_ = h.entries.MarkRead(r.Context(), uid, []core.ID{id}, true)
	// Single-entry: direct feed lookup (only one feed involved).
	feedTitle := h.singleFeedTitle(r.Context(), e.FeedID)
	vm := toEntryVM(e, feedTitle)
	if err := h.tmpl["entry"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "entry/layout", "error", err)
	}
}

func (h *Handler) listFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.feeds.List(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := h.tmpl["feeds"].ExecuteTemplate(w, "layout", map[string]any{"Feeds": feeds}); err != nil {
		h.log.Error("template execute", "template", "feeds/layout", "error", err)
	}
}

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	if _, err := h.feeds.Subscribe(r.Context(), uid, r.FormValue("url")); err != nil {
		http.Error(w, "subscribe failed: "+err.Error(), 422)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	_ = h.feeds.Refresh(r.Context(), uid, id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.feeds.Delete(r.Context(), uid, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) toggleRead(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	e, err := h.entries.Get(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	read := e.Status != core.StatusRead
	_ = h.entries.MarkRead(r.Context(), uid, []core.ID{id}, read)
	if updated, err := h.entries.Get(r.Context(), uid, id); err == nil {
		e = updated
	}
	feedTitle := h.singleFeedTitle(r.Context(), e.FeedID)
	if err := h.tmpl["entryrow"].ExecuteTemplate(w, "entryrow", toEntryVM(e, feedTitle)); err != nil {
		h.log.Error("template execute", "template", "entryrow/entryrow", "error", err)
	}
}

func (h *Handler) toggleStar(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	e, err := h.entries.Get(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = h.entries.Star(r.Context(), uid, []core.ID{id}, !e.Starred)
	if updated, err := h.entries.Get(r.Context(), uid, id); err == nil {
		e = updated
	}
	feedTitle := h.singleFeedTitle(r.Context(), e.FeedID)
	if err := h.tmpl["entryrow"].ExecuteTemplate(w, "entryrow", toEntryVM(e, feedTitle)); err != nil {
		h.log.Error("template execute", "template", "entryrow/entryrow", "error", err)
	}
}

func (h *Handler) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	_ = h.entries.Delete(r.Context(), uid, id)
	w.WriteHeader(http.StatusOK)
}

// feedTitleMap fetches all feeds for the default user and returns a map of feed ID → title.
// Used by renderList to do a single feeds.List call for the whole page rather than one per entry.
func (h *Handler) feedTitleMap(ctx context.Context) map[core.ID]string {
	feeds, err := h.feeds.List(ctx, uid)
	if err != nil {
		return map[core.ID]string{}
	}
	m := make(map[core.ID]string, len(feeds))
	for _, f := range feeds {
		m[f.ID] = f.Title
	}
	return m
}

// singleFeedTitle looks up the title for one feed; used by single-entry handlers.
func (h *Handler) singleFeedTitle(ctx context.Context, feedID core.ID) string {
	feeds, err := h.feeds.List(ctx, uid)
	if err != nil {
		return ""
	}
	for _, f := range feeds {
		if f.ID == feedID {
			return f.Title
		}
	}
	return ""
}

func parseID(w http.ResponseWriter, r *http.Request) (core.ID, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return 0, false
	}
	return core.ID(n), true
}

func toEntryVMs(es []*core.Entry, feedTitles map[core.ID]string) []entryVM {
	out := make([]entryVM, 0, len(es))
	for _, e := range es {
		out = append(out, toEntryVM(e, feedTitles[e.FeedID]))
	}
	return out
}

func toEntryVM(e *core.Entry, feedTitle string) entryVM {
	return entryVM{
		ID:        e.ID,
		Title:     e.Title,
		URL:       e.URL,
		Author:    e.Author,
		Content:   templateHTML(e.Content),
		Status:    string(e.Status),
		Starred:   e.Starred,
		FeedID:    e.FeedID,
		FeedTitle: feedTitle,
		Published: e.PublishedAt.Format("2006-01-02 15:04"),
	}
}
