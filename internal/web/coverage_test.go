package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func seedFeedWithEntry(t *testing.T, store *coretest.MemStore, guid, title string) (core.ID, core.ID) {
	t.Helper()
	ctx := context.Background()
	fid, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/" + guid, Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: guid, Title: title, Content: "<p>body</p>", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})
	if err != nil || len(ins) != 1 {
		t.Fatalf("seed entry: ins=%d err=%v", len(ins), err)
	}
	return fid, ins[0].ID
}

// GET /entries/{id} must mark the entry read on open — a load-bearing invariant the
// reader's mutation redirects are designed around (CLAUDE.md).
func TestEntryOpenMarksRead(t *testing.T) {
	h, store := newWeb(t)
	_, eid := seedFeedWithEntry(t, store, "g", "Post")

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(eid), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := store.GetEntry(context.Background(), core.DefaultUserID, eid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.StatusRead {
		t.Fatalf("opening entry did not mark it read: status=%s", got.Status)
	}
	if got.ReadAt == nil {
		t.Fatal("opening entry left ReadAt nil")
	}
}

// POST /feeds/{id}/delete removes the feed and cascades its entries; the store
// state must reflect that above the store layer.
func TestDeleteFeedRemovesFeedAndEntries(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, eid := seedFeedWithEntry(t, store, "g", "Post")

	req := httptest.NewRequest(http.MethodPost, "/feeds/"+strconv.FormatInt(int64(fid), 10)+"/delete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete feed status %d, want 204", rec.Code)
	}
	if rec.Header().Get("HX-Refresh") != "true" {
		t.Errorf("delete feed should reload the page to refresh group counts (HX-Refresh)")
	}
	if _, err := store.GetFeed(ctx, core.DefaultUserID, fid); err == nil {
		t.Fatal("feed still present after delete")
	}
	if _, err := store.GetEntry(ctx, core.DefaultUserID, eid); err == nil {
		t.Fatal("entry not cascaded after feed delete")
	}
}

func TestDeleteFeedMissingReturnsError(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodPost, "/feeds/999/delete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("deleting a missing feed returned 200; want an error status")
	}
}

// Cursor pagination: the full page renders a Load-more link, and the HX-Request
// cursor branch returns only the bare entrylist fragment (no layout).
func TestCursorPaginationLoadMore(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	batch := make([]*core.Entry, 0, 51)
	for i := 0; i < 51; i++ {
		batch = append(batch, &core.Entry{UserID: core.DefaultUserID, FeedID: fid, GUID: "g" + strconv.Itoa(i), Title: "post " + strconv.Itoa(i), Status: core.StatusUnread, PublishedAt: time.Unix(int64(1000+i), 0)})
	}
	store.UpsertEntries(ctx, fid, batch)

	// Full page: 51 entries > 50-row page ⇒ a Load-more link with a cursor.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "Load more") {
		t.Fatalf("expected Load more link on overflowing list:\n%s", body)
	}
	// The button must replace itself on click (hx-target="this" + outerHTML), not
	// append into #entries — otherwise the old button is stranded mid-list and a
	// second click re-appends the same page, duplicating entries.
	if !strings.Contains(body, `hx-target="this"`) || !strings.Contains(body, `hx-swap="outerHTML"`) {
		t.Fatalf("Load more button must self-replace (hx-target=this hx-swap=outerHTML):\n%s", body)
	}
	m := regexp.MustCompile(`\?cursor=([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("could not find cursor in Load more link:\n%s", body)
	}
	cursor := m[1]

	// Second page as an htmx load-more request: bare fragment, no full layout.
	req2 := httptest.NewRequest(http.MethodGet, "/?cursor="+cursor, nil)
	req2.Header.Set("HX-Request", "true")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("cursor page status %d", rec2.Code)
	}
	frag := rec2.Body.String()
	if !strings.Contains(frag, `id="entries"`) {
		t.Fatalf("cursor fragment missing entrylist container:\n%s", frag)
	}
	if strings.Contains(frag, "<html") || strings.Contains(frag, "<body") {
		t.Fatalf("cursor fragment must not render the full layout:\n%s", frag)
	}
	// The 51st entry (oldest, "post 0") belongs on page two, not page one.
	if !strings.Contains(frag, "post 0<") && !strings.Contains(frag, "post 0</a>") {
		t.Fatalf("second page missing the 51st entry:\n%s", frag)
	}
}

// A malformed cursor decodes to nil (first page) and must not 500.
func TestMalformedCursorDoesNotError(t *testing.T) {
	h, store := newWeb(t)
	seedFeedWithEntry(t, store, "g", "Post")
	req := httptest.NewRequest(http.MethodGet, "/?cursor=!!!not-base64!!!", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("malformed cursor status %d, want 200 (treated as first page)", rec.Code)
	}
}

func TestStarredViewListsOnlyStarred(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "StarMe", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g2", Title: "PlainOne", Status: core.StatusUnread, PublishedAt: time.Unix(200, 0)},
	})
	if err := store.SetStarred(ctx, core.DefaultUserID, []core.ID{ins[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/starred", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("starred status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "StarMe") {
		t.Fatalf("starred view missing starred entry:\n%s", body)
	}
	if strings.Contains(body, "PlainOne") {
		t.Fatalf("starred view leaked an unstarred entry:\n%s", body)
	}
}

func TestUncategorisedViewListsOnlyUncategorised(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	catFeed, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", Title: "A", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	noneFeed, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b/f", Title: "B", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, catFeed, []*core.Entry{{UserID: core.DefaultUserID, FeedID: catFeed, GUID: "c", Title: "InCategory", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})
	store.UpsertEntries(ctx, noneFeed, []*core.Entry{{UserID: core.DefaultUserID, FeedID: noneFeed, GUID: "n", Title: "NoCategory", Status: core.StatusUnread, PublishedAt: time.Unix(200, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/categories/none", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("uncategorised status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "NoCategory") {
		t.Fatalf("uncategorised view missing its entry:\n%s", body)
	}
	if strings.Contains(body, "InCategory") {
		t.Fatalf("uncategorised view leaked a categorised entry:\n%s", body)
	}
}

func TestRenameCategory(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "Old"})

	form := strings.NewReader("title=New")
	req := httptest.NewRequest(http.MethodPost, "/categories/"+strconv.FormatInt(int64(catID), 10)+"/rename", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("rename status %d, want 303", rec.Code)
	}
	c, err := store.GetCategory(ctx, core.DefaultUserID, catID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "New" {
		t.Fatalf("category not renamed: %q", c.Title)
	}
}

// The list-view (non-reader) delete branch returns a plain 200 (htmx removes the
// row) with no HX-Redirect — distinct from the reader branch (feedback_test.go).
func TestDeleteEntryListViewReturns200(t *testing.T) {
	h, store := newWeb(t)
	_, eid := seedFeedWithEntry(t, store, "g", "Post")

	req := httptest.NewRequest(http.MethodPost, "/entries/"+strconv.FormatInt(int64(eid), 10)+"/delete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list-view delete status %d, want 200", rec.Code)
	}
	if rec.Header().Get("HX-Redirect") != "" {
		t.Fatalf("list-view delete must not redirect; got HX-Redirect=%q", rec.Header().Get("HX-Redirect"))
	}
	if _, err := store.GetEntry(context.Background(), core.DefaultUserID, eid); err == nil {
		t.Fatal("entry not deleted")
	}
}
