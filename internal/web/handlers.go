package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	Summary   string
}

type listVM struct {
	chrome
	Title      string
	ListPath   string
	Entries    []entryVM
	NextCursor string
	Categories []feedsCatVM
}

type entryPageVM struct {
	chrome
	Entry       entryVM
	ReadingTime string
}

type feedsCatVM struct {
	ID    int64
	Title string
}

type feedRowVM struct {
	ID          core.ID
	Title       string
	FeedURL     string
	LastError   string
	CategoryID  int64 // 0 = uncategorised
	FullContent bool
}

type feedGroupVM struct {
	Title string
	Feeds []feedRowVM
}

type feedsPageVM struct {
	chrome
	Categories []feedsCatVM
	Groups     []feedGroupVM
	HasFeeds   bool
}

func (h *Handler) unread(w http.ResponseWriter, r *http.Request) {
	st := core.StatusUnread
	h.renderList(w, r, "Unread", "/", core.EntryFilter{Status: &st})
}

func (h *Handler) starred(w http.ResponseWriter, r *http.Request) {
	star := true
	h.renderList(w, r, "Starred", "/starred", core.EntryFilter{Starred: &star})
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, "History", "/history", core.EntryFilter{Order: core.OrderReadAtDesc})
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
	// Category options are only needed by the subscribe form on the full page,
	// never by the entrylist fragment above — fetch them only here.
	vm.Categories = h.catVMs(r.Context())
	vm.chrome = h.chromeFor(r, listActive(f))
	if err := h.tmpl["entries"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "entries/layout", "error", err)
	}
}

// listActive maps a list filter to its nav highlight key.
func listActive(f core.EntryFilter) string {
	switch {
	case f.Starred != nil && *f.Starred:
		return "starred"
	case f.Order == core.OrderReadAtDesc:
		return "history"
	case f.CategoryID != nil || f.Uncategorised:
		return "categories"
	case f.FeedID != nil:
		return "feeds"
	default:
		return "unread"
	}
}

// catVMs returns the user's categories as select-option view models for the
// subscribe form; a store error degrades to no options (logged, non-fatal).
func (h *Handler) catVMs(ctx context.Context) []feedsCatVM {
	cats, err := h.cats.List(ctx, uid)
	if err != nil {
		h.log.Warn("list categories for subscribe form", "error", err)
		return nil
	}
	return toCatVMs(cats)
}

func toCatVMs(cats []*core.Category) []feedsCatVM {
	out := make([]feedsCatVM, 0, len(cats))
	for _, c := range cats {
		out = append(out, feedsCatVM{ID: int64(c.ID), Title: c.Title})
	}
	return out
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
	if err := h.entries.MarkRead(r.Context(), uid, []core.ID{id}, true); err != nil {
		h.log.Warn("mark read on open", "entry_id", int64(id), "error", err)
	}
	// Single-entry: direct feed lookup (only one feed involved).
	feedTitle := h.singleFeedTitle(r.Context(), e.FeedID)
	ev := toEntryVM(e, feedTitle)
	vm := entryPageVM{Entry: ev, ReadingTime: readingTime(string(ev.Content))}
	vm.chrome = h.chromeFor(r, "")
	if err := h.tmpl["entry"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "entry/layout", "error", err)
	}
}

func (h *Handler) listFeeds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	feeds, err := h.feeds.List(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cats, err := h.cats.List(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	row := func(f *core.Feed) feedRowVM {
		var cid int64
		if f.CategoryID != nil {
			cid = int64(*f.CategoryID)
		}
		return feedRowVM{ID: f.ID, Title: f.Title, FeedURL: f.FeedURL, LastError: f.LastError, CategoryID: cid, FullContent: f.FetchFullContent}
	}
	byCat := map[core.ID][]feedRowVM{}
	var uncat []feedRowVM
	for _, f := range feeds {
		if f.CategoryID == nil {
			uncat = append(uncat, row(f))
		} else {
			byCat[*f.CategoryID] = append(byCat[*f.CategoryID], row(f))
		}
	}
	vm := feedsPageVM{HasFeeds: len(feeds) > 0, Categories: toCatVMs(cats)}
	// Only render groups that actually contain feeds — an empty heading with a
	// "No feeds." line under it is noise (the HasFeeds gate covers no-feeds-at-all).
	for _, c := range cats {
		if rows := byCat[c.ID]; len(rows) > 0 {
			vm.Groups = append(vm.Groups, feedGroupVM{Title: c.Title, Feeds: rows})
		}
	}
	if len(uncat) > 0 {
		vm.Groups = append(vm.Groups, feedGroupVM{Title: "Uncategorised", Feeds: uncat})
	}
	vm.chrome = h.chromeFor(r, "feeds")
	if err := h.tmpl["feeds"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "feeds/layout", "error", err)
	}
}

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	catID, ok := parseCategoryID(r)
	if !ok {
		http.Error(w, "bad category id", http.StatusBadRequest)
		return
	}
	full := r.FormValue("full_content") == "on"
	if _, err := h.feeds.Subscribe(r.Context(), uid, r.FormValue("url"), catID, full); err != nil {
		http.Error(w, "subscribe failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) setFeedFullContent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	on := r.FormValue("full_content") == "on"
	if err := h.feeds.SetFullContent(r.Context(), uid, id, on); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	// The toggle's hx-vals is baked from the old state at render time, so reload
	// the feeds page to re-render the button — otherwise it keeps posting the
	// same value and the toggle is one-way.
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) setFeedCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	catID, ok := parseCategoryID(r)
	if !ok {
		http.Error(w, "bad category id", http.StatusBadRequest)
		return
	}
	if err := h.feeds.SetCategory(r.Context(), uid, id, catID); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseCategoryID reads the optional category_id form field. Empty → (nil, true)
// meaning uncategorised. A valid positive id → (&id, true). A malformed or
// non-positive value → (nil, false) so the caller rejects the request rather
// than silently clearing the category.
func parseCategoryID(r *http.Request) (*core.ID, bool) {
	v := strings.TrimSpace(r.FormValue("category_id"))
	if v == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return nil, false
	}
	id := core.ID(n)
	return &id, true
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.feeds.Refresh(r.Context(), uid, id); err != nil {
		h.log.Warn("refresh feed", "feed_id", int64(id), "error", err)
	}
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

// toggleEntry is the shared skeleton for toggleRead and toggleStar.
// It fetches the entry, calls mutate (which performs the state change), re-fetches
// the (possibly updated) entry, and renders the entryrow fragment.
func (h *Handler) toggleEntry(w http.ResponseWriter, r *http.Request, mutate func(ctx context.Context, id core.ID, cur *core.Entry) error) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	e, err := h.entries.Get(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := mutate(r.Context(), id, e); err != nil {
		h.log.Warn("toggle entry", "entry_id", int64(id), "error", err)
	}
	if updated, err := h.entries.Get(r.Context(), uid, id); err == nil {
		e = updated
	}
	feedTitle := h.singleFeedTitle(r.Context(), e.FeedID)
	if err := h.tmpl["entryrow"].ExecuteTemplate(w, "entryrow", toEntryVM(e, feedTitle)); err != nil {
		h.log.Error("template execute", "template", "entryrow/entryrow", "error", err)
	}
}

func (h *Handler) toggleRead(w http.ResponseWriter, r *http.Request) {
	h.toggleEntry(w, r, func(ctx context.Context, id core.ID, cur *core.Entry) error {
		read := cur.Status != core.StatusRead
		return h.entries.MarkRead(ctx, uid, []core.ID{id}, read)
	})
}

func (h *Handler) toggleStar(w http.ResponseWriter, r *http.Request) {
	h.toggleEntry(w, r, func(ctx context.Context, id core.ID, cur *core.Entry) error {
		return h.entries.Star(ctx, uid, []core.ID{id}, !cur.Starred)
	})
}

func (h *Handler) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.entries.Delete(r.Context(), uid, id); err != nil {
		h.log.Warn("delete entry", "entry_id", int64(id), "error", err)
	}
	w.WriteHeader(http.StatusOK)
}

// feedTitleMap fetches all feeds for the default user and returns a map of feed ID → title.
// Used by renderList to do a single feeds.List call for the whole page rather than one per entry.
func (h *Handler) feedTitleMap(ctx context.Context) map[core.ID]string {
	feeds, err := h.feeds.List(ctx, uid)
	if err != nil {
		h.log.Error("list feeds for title map", "error", err)
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
	f, err := h.feeds.Get(ctx, uid, feedID)
	if err != nil {
		h.log.Warn("get feed for title", "feed_id", int64(feedID), "error", err)
		return ""
	}
	return f.Title
}

type categoryVM struct {
	ID     core.ID
	Title  string
	Unread int
}

type categoriesPageVM struct {
	chrome
	Categories    []categoryVM
	Uncategorised int
}

func (h *Handler) categoriesIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cats, err := h.cats.List(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	counts, uncat, err := h.cats.UnreadCounts(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	vm := categoriesPageVM{Uncategorised: uncat}
	for _, c := range cats {
		vm.Categories = append(vm.Categories, categoryVM{ID: c.ID, Title: c.Title, Unread: counts[c.ID]})
	}
	vm.chrome = h.chromeFor(r, "categories")
	if err := h.tmpl["categories"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "categories/layout", "error", err)
	}
}

func (h *Handler) categoryEntries(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	c, err := h.cats.Get(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.renderList(w, r, c.Title, "/categories/"+strconv.FormatInt(int64(id), 10), core.EntryFilter{CategoryID: &id})
}

func (h *Handler) uncategorisedEntries(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, "Uncategorised", "/categories/none", core.EntryFilter{Uncategorised: true})
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	if _, err := h.cats.Create(r.Context(), uid, r.FormValue("title")); err != nil {
		http.Error(w, "create category failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/categories", http.StatusSeeOther)
}

func (h *Handler) renameCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.cats.Rename(r.Context(), uid, id, r.FormValue("title")); err != nil {
		http.Error(w, "rename failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/categories", http.StatusSeeOther)
}

func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.cats.Delete(r.Context(), uid, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type searchVM struct {
	chrome
	Query      string
	Header     string
	Entries    []entryVM
	NextCursor string // always "" — search has no pagination; satisfies the entrylist template
}

func (h *Handler) searchHandler(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	vm := searchVM{Query: q}
	if q != "" {
		entries, _, err := h.search.Search(r.Context(), uid, q, core.EntryFilter{})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if len(entries) > 0 {
			vm.Entries = toEntryVMs(entries, h.feedTitleMap(r.Context()))
		}
		// The store caps at 50 (relevance-ranked, no pagination this iteration), so
		// len==50 means "at the cap" — phrase it as the top 50 by relevance rather
		// than implying the rest were truncated.
		switch n := len(entries); {
		case n >= 50:
			vm.Header = fmt.Sprintf("Search: %s — top 50 matches (refine to narrow)", q)
		case n == 1:
			vm.Header = fmt.Sprintf("Search: %s — 1 match", q)
		default:
			vm.Header = fmt.Sprintf("Search: %s — %d matches", q, n)
		}
	}
	vm.chrome = h.chromeFor(r, "search")
	if err := h.tmpl["search"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "search/layout", "error", err)
	}
}

type settingsOption struct{ Value, Label string }

// Option-slice fields are named *Opts so they never collide with the promoted
// chrome.Summaries / chrome.Width string fields (which carry the current value).
type settingsPageVM struct {
	chrome
	ThemeChoice string // "system" (not "") so the radio matches
	ThemeOpts   []settingsOption
	SummaryOpts []settingsOption
	WidthOpts   []settingsOption
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	c := h.chromeFor(r, "settings")
	themeChoice := c.Theme
	if themeChoice == "" {
		themeChoice = "system"
	}
	vm := settingsPageVM{
		chrome:      c,
		ThemeChoice: themeChoice,
		ThemeOpts:   []settingsOption{{"system", "System"}, {"light", "Light"}, {"sepia", "Sepia"}, {"dark", "Dark"}},
		SummaryOpts: []settingsOption{{"show", "Show"}, {"hide", "Hide"}},
		WidthOpts:   []settingsOption{{"comfortable", "Comfortable"}, {"wide", "Wide"}},
	}
	if err := h.tmpl["settings"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "settings/layout", "error", err)
	}
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	pick := func(field, def string, allowed ...string) string {
		v := r.FormValue(field)
		for _, a := range allowed {
			if v == a {
				return v
			}
		}
		return def
	}
	setPrefCookie(w, "bfeed_theme", pick("theme", "system", "system", "light", "sepia", "dark"))
	setPrefCookie(w, "bfeed_summary", pick("summary", "show", "show", "hide"))
	setPrefCookie(w, "bfeed_width", pick("width", "comfortable", "comfortable", "wide"))
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
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
	// Prefer full content; fall back to summary when a feed only ships
	// <summary>/<description> (atom summary-only feeds, most RSS). Both are
	// already sanitised in the store, so either is safe to render as HTML.
	body := e.Content
	if body == "" {
		body = e.Summary
	}
	return entryVM{
		ID:        e.ID,
		Title:     e.Title,
		URL:       e.URL,
		Author:    e.Author,
		Content:   templateHTML(body),
		Status:    string(e.Status),
		Starred:   e.Starred,
		FeedID:    e.FeedID,
		FeedTitle: feedTitle,
		Published: humanizeSince(e.PublishedAt, time.Now()),
		Summary:   summaryText(e),
	}
}
