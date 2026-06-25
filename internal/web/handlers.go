package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

const uid = core.DefaultUserID

type entryVM struct {
	ID            core.ID
	Title         string
	URL           string
	Author        string
	Content       templateHTML
	Status        string
	Starred       bool
	FeedID        core.ID
	FeedTitle     string
	Published     string
	PublishedFull string // full date+time, shown as the hover tooltip
	PublishedAttr string // RFC3339, the machine-readable <time datetime>
	Summary       string
}

type listVM struct {
	chrome
	Title        string
	ListPath     string
	MarkReadPath string // non-empty only on the single-feed view → renders the "Mark all read" button
	Entries      []entryVM
	NextCursor   string
	Empty        string // empty-state headline (shown when Entries is empty)
	EmptySub     string // optional faint empty-state subline
	HeaderCount  string // preformatted header count, e.g. "23 unread"; empty hides it
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

type feedEditCatVM struct {
	ID       int64
	Title    string
	Selected bool
}

type feedRowVM struct {
	ID          core.ID
	Title       string // display title (user override or poll-owned)
	FeedURL     string
	Host        string
	LastError   string
	EditError   string // validation/save error from the inline edit form
	CategoryID  int64  // 0 = uncategorised
	FullContent bool
	Unread      int
	Total       int
	Stalled     bool   // error_count >= configured error limit
	Updated     string // "2h ago"; "" if never checked
	Next        string // "in 1h"; "" if past/unknown
	Refreshing  bool   // background refresh in flight (has CheckedAt)
	Pending     bool   // background subscribe in flight (no CheckedAt yet)
	Editing     bool   // edit form is open
	Cats        []feedEditCatVM
}

type feedGroupVM struct {
	CatID     int64
	Title     string
	FeedCount int
	Unread    int
	OOB       bool
	Feeds     []feedRowVM
}

type feedGroupHeadVM struct {
	CatID     int64
	Title     string
	FeedCount int
	Unread    int
	OOB       bool
}

type feedsPageVM struct {
	chrome
	Categories []feedsCatVM
	Groups     []feedGroupVM
	HasFeeds   bool
	ShowCounts bool // false when the stats lookup failed → omit per-feed counts
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
	if f.FeedID != nil {
		vm.MarkReadPath = path + "/mark-read" // path is "/feeds/{id}" → "/feeds/{id}/mark-read"
	}
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
	vm.Empty, vm.EmptySub = emptyFor(f)
	// HeaderCount is best-effort chrome: a failed stats lookup omits the count
	// (the list itself still renders) but is logged so the failure isn't silent.
	switch {
	case listActive(f) == "unread":
		if stats, err := h.feeds.EntryStats(r.Context(), uid); err != nil {
			h.log.Warn("header unread count", "error", err)
		} else {
			total := 0
			for _, s := range stats {
				total += s.Unread
			}
			vm.HeaderCount = fmt.Sprintf("%d unread", total)
		}
	case f.FeedID != nil:
		if stats, err := h.feeds.EntryStats(r.Context(), uid); err != nil {
			h.log.Warn("header feed count", "feed_id", int64(*f.FeedID), "error", err)
		} else {
			s := stats[*f.FeedID]
			vm.HeaderCount = fmt.Sprintf("%d unread · %d total", s.Unread, s.Total)
		}
	}
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

// emptyFor returns the empty-state copy for a list view. Copy deliberately
// avoids the internal words "entry"/"entries".
func emptyFor(f core.EntryFilter) (msg, sub string) {
	switch listActive(f) {
	case "starred":
		return "Nothing saved yet.", "Tap the star to keep things here."
	case "history":
		return "Nothing read yet.", ""
	case "unread":
		return "You're all caught up.", ""
	default: // single feed, category, uncategorised
		return "Nothing here yet.", ""
	}
}

func feedHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return u.Host
	}
	return rawURL
}

func (h *Handler) buildFeedRow(f *core.Feed, st core.FeedEntryStats, now time.Time) feedRowVM {
	var cid int64
	if f.CategoryID != nil {
		cid = int64(*f.CategoryID)
	}
	inFlight := h.busy.has(f.ID)
	row := feedRowVM{
		ID: f.ID, Title: f.DisplayTitle(), FeedURL: f.FeedURL, Host: feedHost(f.FeedURL),
		LastError: f.LastError, CategoryID: cid, FullContent: f.FetchFullContent,
		Unread: st.Unread, Total: st.Total, Stalled: f.ErrorCount >= h.errorLimit,
		Next: humanizeUntil(f.NextCheckAt, now),
	}
	if f.CheckedAt != nil {
		row.Updated = humanizeSince(*f.CheckedAt, now)
	}
	if inFlight {
		if f.CheckedAt == nil {
			row.Pending = true
		} else {
			row.Refreshing = true
		}
	}
	return row
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
	// Reading time from the original content, before the image-proxy rewrite
	// lengthens img src URLs (the rewrite must not skew the estimate).
	readMin := readingTime(string(ev.Content))
	if h.imgRewrite != nil {
		ev.Content = templateHTML(proxifyImages(string(ev.Content), h.imgRewrite))
	}
	vm := entryPageVM{Entry: ev, ReadingTime: readMin}
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
	// Counts are additive chrome: on a stats error, log and render the page
	// without counts rather than failing the whole feed list (a nil map reads
	// as the zero value, which ShowCounts then hides).
	stats, statsErr := h.feeds.EntryStats(ctx, uid)
	if statsErr != nil {
		h.log.Warn("feed entry stats", "error", statsErr)
	}
	now := time.Now()
	row := func(f *core.Feed) feedRowVM { return h.buildFeedRow(f, stats[f.ID], now) }
	byCat := map[core.ID][]feedRowVM{}
	var uncat []feedRowVM
	for _, f := range feeds {
		if f.CategoryID == nil {
			uncat = append(uncat, row(f))
		} else {
			byCat[*f.CategoryID] = append(byCat[*f.CategoryID], row(f))
		}
	}
	vm := feedsPageVM{HasFeeds: len(feeds) > 0, Categories: toCatVMs(cats), ShowCounts: statsErr == nil}
	// Only render groups that actually contain feeds — an empty heading with a
	// "No feeds." line under it is noise (the HasFeeds gate covers no-feeds-at-all).
	mkGroup := func(catID int64, title string, rows []feedRowVM) feedGroupVM {
		g := feedGroupVM{CatID: catID, Title: title, Feeds: rows, FeedCount: len(rows)}
		for _, rr := range rows {
			g.Unread += rr.Unread
		}
		return g
	}
	for _, c := range cats {
		if rows := byCat[c.ID]; len(rows) > 0 {
			vm.Groups = append(vm.Groups, mkGroup(int64(c.ID), c.Title, rows))
		}
	}
	if len(uncat) > 0 {
		vm.Groups = append(vm.Groups, mkGroup(0, "Uncategorised", uncat))
	}
	vm.chrome = h.chromeFor(r, "feeds")
	if err := h.tmpl["feeds"].ExecuteTemplate(w, "layout", vm); err != nil {
		h.log.Error("template execute", "template", "feeds/layout", "error", err)
	}
}

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	catID, ok := parseCategoryID(r)
	if !ok {
		h.renderSubscribeError(w, "Invalid category.")
		return
	}
	full := r.FormValue("full_content") == "on"
	f, err := h.feeds.CreateSubscription(r.Context(), uid, r.FormValue("url"), catID, full)
	if err != nil {
		h.renderSubscribeError(w, "Couldn't add feed: "+err.Error())
		return
	}
	// Resolve + ingest in the background; the reloaded page shows a pending row
	// that polls until the feed populates (or turns into an error row).
	// context.Background() is intentional: the goroutine must outlive the request.
	if h.busy.start(f.ID) {
		go func() { //nolint:gosec // G118: background goroutine intentionally outlives request; context.Background() is correct here
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			defer h.busy.done(f.ID)
			if err := h.feeds.ResolveAndIngest(ctx, f); err != nil {
				h.log.Warn("background subscribe", "feed_id", int64(f.ID), "error", err)
			}
		}()
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// renderSubscribeError returns the inline error fragment (auto-escaped) with a
// 200 so htmx swaps it; the typed URL stays because only the message area swaps.
func (h *Handler) renderSubscribeError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl["feeds"].ExecuteTemplate(w, "subscribeError", msg); err != nil {
		h.log.Error("template execute", "template", "feeds/subscribeError", "error", err)
	}
}

// catOptions builds the category dropdown items for the inline edit form,
// marking the currently selected category.
func (h *Handler) catOptions(ctx context.Context, selected *core.ID) []feedEditCatVM {
	cats, err := h.cats.List(ctx, uid)
	if err != nil {
		return nil
	}
	out := make([]feedEditCatVM, 0, len(cats))
	for _, c := range cats {
		out = append(out, feedEditCatVM{
			ID:       int64(c.ID),
			Title:    c.Title,
			Selected: selected != nil && *selected == c.ID,
		})
	}
	return out
}

// feedEditForm handles GET /feeds/{id}/edit: renders the feed row with its
// edit panel expanded so the user can modify title, URL, category, and full-content preference.
func (h *Handler) feedEditForm(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	f, err := h.feeds.Get(ctx, uid, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	stats, _ := h.feeds.EntryStats(ctx, uid)
	row := h.buildFeedRow(f, stats[id], time.Now())
	row.Editing = true
	row.Cats = h.catOptions(ctx, f.CategoryID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl["feedrow"].ExecuteTemplate(w, "feedrow", row); err != nil {
		h.log.Error("template execute", "template", "feedrow/edit", "error", err)
	}
}

// editFeed handles POST /feeds/{id}: unified save for the inline edit panel.
// On category change → HX-Refresh (row moves groups). On URL change →
// background re-resolve then row swap. Otherwise → row swap.
func (h *Handler) editFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	catID, ok := parseCategoryID(r)
	if !ok {
		http.Error(w, "bad category id", http.StatusBadRequest)
		return
	}
	in := core.EditFeedInput{
		Title:       r.FormValue("title"),
		URL:         r.FormValue("url"),
		CategoryID:  catID,
		FullContent: r.FormValue("full_content") == "on",
	}
	res, err := h.feeds.EditFeed(r.Context(), uid, id, in)
	if err != nil {
		h.renderEditError(w, r, id, err)
		return
	}
	if res.CategoryChanged {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if res.URLChanged {
		h.startRefresh(id)
	}
	h.renderFeedRow(w, r, id)
}

// renderEditError re-renders the edit panel with an inline error message and
// returns status 422 so htmx swaps it in without treating it as a success.
func (h *Handler) renderEditError(w http.ResponseWriter, r *http.Request, id core.ID, cause error) {
	ctx := r.Context()
	f, gerr := h.feeds.Get(ctx, uid, id)
	if gerr != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	stats, _ := h.feeds.EntryStats(ctx, uid)
	row := h.buildFeedRow(f, stats[id], time.Now())
	row.Editing = true
	row.Cats = h.catOptions(ctx, f.CategoryID)
	row.EditError = "Couldn't save: " + cause.Error()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := h.tmpl["feedrow"].ExecuteTemplate(w, "feedrow", row); err != nil {
		h.log.Error("template execute", "template", "feedrow/editerr", "error", err)
	}
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
	h.startRefresh(id)
	h.renderFeedRow(w, r, id)
}

// startRefresh spawns a background poll if one is not already running for id.
func (h *Handler) startRefresh(id core.ID) {
	if !h.busy.start(id) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		defer h.busy.done(id)
		if err := h.feeds.Refresh(ctx, uid, id); err != nil {
			h.log.Warn("background refresh", "feed_id", int64(id), "error", err)
		}
	}()
}

// renderFeedRow renders the single-row fragment for id. When the feed is not in
// flight it also emits an OOB group-head update so aggregate counts stay fresh
// without a full reload.
func (h *Handler) renderFeedRow(w http.ResponseWriter, r *http.Request, id core.ID) {
	ctx := r.Context()
	f, err := h.feeds.Get(ctx, uid, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	stats, _ := h.feeds.EntryStats(ctx, uid)
	now := time.Now()
	row := h.buildFeedRow(f, stats[id], now)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl["feedrow"].ExecuteTemplate(w, "feedrow", row); err != nil {
		h.log.Error("template execute", "template", "feedrow", "error", err)
		return
	}
	if !row.Refreshing && !row.Pending {
		h.writeGroupHeadOOB(ctx, w, f, stats, now)
	}
}

// feedRow handles GET /feeds/{id}/row — the self-polling target used by the
// refreshing row fragment.
func (h *Handler) feedRow(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	h.renderFeedRow(w, r, id)
}

// writeGroupHeadOOB renders an out-of-band swap for the feed's category group
// head with recomputed feed and unread counts.
func (h *Handler) writeGroupHeadOOB(ctx context.Context, w http.ResponseWriter, f *core.Feed, stats map[core.ID]core.FeedEntryStats, _ time.Time) {
	feeds, err := h.feeds.List(ctx, uid)
	if err != nil {
		return
	}
	var catID int64
	if f.CategoryID != nil {
		catID = int64(*f.CategoryID)
	}
	head := feedGroupHeadVM{CatID: catID, OOB: true, Title: "Uncategorised"}
	if catID != 0 {
		if c, err := h.cats.Get(ctx, uid, core.ID(catID)); err == nil {
			head.Title = c.Title
		}
	}
	for _, ff := range feeds {
		var fc int64
		if ff.CategoryID != nil {
			fc = int64(*ff.CategoryID)
		}
		if fc == catID {
			head.FeedCount++
			head.Unread += stats[ff.ID].Unread
		}
	}
	_ = h.tmpl["feedrow"].ExecuteTemplate(w, "feedgrouphead", head)
}

func (h *Handler) markFeedRead(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	n, err := h.entries.MarkAllRead(r.Context(), uid, core.EntryFilter{FeedID: &id})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.log.Info("mark feed read", "feed_id", int64(id), "count", n)
	// htmx reloads the page so every unread count, row styling, and button state
	// stays consistent without fragment targeting.
	w.Header().Set("HX-Refresh", "true")
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
	if r.FormValue("from") == "reader" {
		h.readerMarkUnread(w, r)
		return
	}
	h.toggleEntry(w, r, func(ctx context.Context, id core.ID, cur *core.Entry) error {
		read := cur.Status != core.StatusRead
		return h.entries.MarkRead(ctx, uid, []core.ID{id}, read)
	})
}

// readerMarkUnread re-queues an entry to unread from the reader and sends the
// client back to Unread. It must NOT re-render the reader: GET /entries/{id}
// marks the entry read on open, which would immediately undo the unread.
func (h *Handler) readerMarkUnread(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.entries.MarkRead(r.Context(), uid, []core.ID{id}, false); err != nil {
		h.log.Warn("reader mark unread", "entry_id", int64(id), "error", err)
	}
	w.Header().Set("HX-Redirect", "/")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) toggleStar(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("from") == "reader" {
		h.readerToggleStar(w, r)
		return
	}
	h.toggleEntry(w, r, func(ctx context.Context, id core.ID, cur *core.Entry) error {
		return h.entries.Star(ctx, uid, []core.ID{id}, !cur.Starred)
	})
}

// readerToggleStar flips the star and returns only the reader star button so the
// reading position is preserved (no full reload).
func (h *Handler) readerToggleStar(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	e, err := h.entries.Get(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	starred := !e.Starred
	if err := h.entries.Star(r.Context(), uid, []core.ID{id}, starred); err != nil {
		h.log.Warn("reader toggle star", "entry_id", int64(id), "error", err)
		starred = e.Starred // render the unchanged state on failure
	}
	e.Starred = starred
	// readerstar renders only .ID and .Starred, so no feed-title lookup is needed.
	if err := h.tmpl["entry"].ExecuteTemplate(w, "readerstar", toEntryVM(e, "")); err != nil {
		h.log.Error("template execute", "template", "entry/readerstar", "error", err)
	}
}

func (h *Handler) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.entries.Delete(r.Context(), uid, id); err != nil {
		h.log.Warn("delete entry", "entry_id", int64(id), "error", err)
	}
	// From the reader there is no row to remove — send the client to Unread.
	// 204 + HX-Redirect mirrors readerMarkUnread (no body to swap).
	if r.FormValue("from") == "reader" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusNoContent)
		return
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
	setPrefCookie(w, "bfeed_theme", allowedOr(r.FormValue("theme"), "system", prefThemes))
	setPrefCookie(w, "bfeed_summary", allowedOr(r.FormValue("summary"), "show", prefSummaries))
	setPrefCookie(w, "bfeed_width", allowedOr(r.FormValue("width"), "comfortable", prefWidths))
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
		ID:            e.ID,
		Title:         e.Title,
		URL:           e.URL,
		Author:        e.Author,
		Content:       templateHTML(body),
		Status:        string(e.Status),
		Starred:       e.Starred,
		FeedID:        e.FeedID,
		FeedTitle:     feedTitle,
		Published:     humanizeSince(e.PublishedAt, time.Now()),
		PublishedFull: e.PublishedAt.Format("2 Jan 2006, 15:04"),
		PublishedAttr: e.PublishedAt.Format(time.RFC3339),
		Summary:       summaryText(e),
	}
}
