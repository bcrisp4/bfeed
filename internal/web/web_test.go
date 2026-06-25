package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
	"github.com/bcrisp4/bfeed/internal/web"
)

// Reuse the shared coretest doubles — no per-package fake duplication.
func newWeb(t *testing.T) (http.Handler, *coretest.MemStore) {
	t.Helper()
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	return web.New(fs, es, cs, ss, log, nil, nil, 20), store
}

func TestUnreadListRenders(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "Hello Post", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Hello Post") {
		t.Fatalf("unread list missing entry:\n%s", rec.Body.String())
	}
}

func TestMarkReadReturnsFragment(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodPost, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10)+"/read", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Mark unread") {
		t.Fatalf("expected toggled fragment, code=%d body=%s", rec.Code, rec.Body.String())
	}
	got, _ := store.GetEntry(ctx, core.DefaultUserID, ins[0].ID)
	if got.Status != core.StatusRead {
		t.Fatal("entry not marked read")
	}
}

func TestHistoryListShowsOnlyReadEntries(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "ReadPost", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g2", Title: "UnreadPost", Status: core.StatusUnread, PublishedAt: time.Unix(200, 0)},
	})
	// Mark the first entry read -> it joins history; the second stays unread.
	if err := store.SetStatus(ctx, core.DefaultUserID, []core.ID{ins[0].ID}, core.StatusRead); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/history", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ReadPost") {
		t.Fatalf("history missing read entry:\n%s", body)
	}
	if strings.Contains(body, "UnreadPost") {
		t.Fatalf("history leaked unread entry:\n%s", body)
	}
}

func TestCategoriesIndexShowsCountsAndUncategorised(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", Title: "A", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/categories", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "News") {
		t.Fatalf("index missing category:\n%s", body)
	}
	if !strings.Contains(body, "Uncategorised") {
		t.Fatalf("index missing uncategorised bucket:\n%s", body)
	}
}

func TestCategoryStreamListsEntries(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", Title: "A", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "CatPost", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/categories/"+strconv.FormatInt(int64(catID), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "CatPost") {
		t.Fatalf("category stream missing entry: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateCategory(t *testing.T) {
	h, store := newWeb(t)
	form := strings.NewReader("title=Tech")
	req := httptest.NewRequest(http.MethodPost, "/categories", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	cats, _ := store.ListCategories(context.Background(), core.DefaultUserID)
	if len(cats) != 1 || cats[0].Title != "Tech" {
		t.Fatalf("category not created: %+v", cats)
	}
}

func TestDeleteCategoryUncategorisesFeeds(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})

	req := httptest.NewRequest(http.MethodPost, "/categories/"+strconv.FormatInt(int64(catID), 10)+"/delete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if f.CategoryID != nil {
		t.Fatalf("feed not uncategorised after category delete")
	}
}

func TestSetFeedCategoryAssignsAndClears(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})

	assign := func(val string) int {
		form := strings.NewReader("category_id=" + val)
		req := httptest.NewRequest(http.MethodPost, "/feeds/"+strconv.FormatInt(int64(fid), 10)+"/category", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := assign(strconv.FormatInt(int64(catID), 10)); code != http.StatusNoContent {
		t.Fatalf("assign status %d", code)
	}
	f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
	if f.CategoryID == nil || *f.CategoryID != catID {
		t.Fatalf("feed not assigned: %v", f.CategoryID)
	}
	if code := assign(""); code != http.StatusNoContent {
		t.Fatalf("clear status %d", code)
	}
	f, _ = store.GetFeed(ctx, core.DefaultUserID, fid)
	if f.CategoryID != nil {
		t.Fatalf("feed not cleared: %v", f.CategoryID)
	}
}

func TestFeedsPageGroupsByCategory(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", Title: "InCat", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b/f", Title: "NoCat", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("feeds page status %d", rec.Code)
	}
	// Group heads are rendered by the feedgrouphead template using id="feed-group-{catID}".
	if !strings.Contains(body, `id="feed-group-`) {
		t.Fatalf("feeds page missing group head id: code=%d\n%s", rec.Code, body)
	}
	if !strings.Contains(body, "News") || !strings.Contains(body, "Uncategorised") {
		t.Fatalf("feeds page missing group titles:\n%s", body)
	}
	if !strings.Contains(body, "InCat") || !strings.Contains(body, "NoCat") {
		t.Fatalf("feeds page missing feeds:\n%s", body)
	}
	// The new add form uses "Add feed", not "Subscribe".
	if !strings.Contains(body, "Add feed") {
		t.Fatalf("feeds page missing 'Add feed' button:\n%s", body)
	}
}

func TestFeedsPageNoOOBInlineGroupHead(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "Tech"})
	store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", Title: "A", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("feeds page status %d\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Inline group heads on the full page must NOT carry hx-swap-oob — that
	// attribute belongs only on OOB fragment responses from GET /feeds/{id}/row.
	if strings.Contains(body, "hx-swap-oob") {
		t.Fatalf("feeds page must not contain hx-swap-oob (OOB only belongs on row fragments):\n%s", body)
	}
	// But the group head id must still be present.
	if !strings.Contains(body, `id="feed-group-`) {
		t.Fatalf("feeds page missing group head id:\n%s", body)
	}
}

func TestFeedsPageEmptyState(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No feeds yet") {
		t.Fatalf("empty state missing 'No feeds yet':\n%s", body)
	}
}

func TestSubscribeFormShowsCategoryOptions(t *testing.T) {
	h, store := newWeb(t)
	store.CreateCategory(context.Background(), &core.Category{UserID: core.DefaultUserID, Title: "Tech"})
	// The subscribe form lives on the Feeds page.
	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Tech") {
		t.Fatalf("subscribe form missing category option:\n%s", rec.Body.String())
	}
}

func TestSetFeedCategoryRejectsMalformedID(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a/f", CategoryID: &catID, NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})

	for _, bad := range []string{"abc", "0", "-5"} {
		form := strings.NewReader("category_id=" + bad)
		req := httptest.NewRequest(http.MethodPost, "/feeds/"+strconv.FormatInt(int64(fid), 10)+"/category", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("category_id=%q status = %d, want 400", bad, rec.Code)
		}
		// A malformed value must not silently clear the existing category.
		f, _ := store.GetFeed(ctx, core.DefaultUserID, fid)
		if f.CategoryID == nil || *f.CategoryID != catID {
			t.Fatalf("category_id=%q changed assignment to %v", bad, f.CategoryID)
		}
	}
}

func TestSearchRendersOnlyMatches(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "Kubernetes networking", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g2", Title: "Go modules", Status: core.StatusUnread, PublishedAt: time.Unix(200, 0)},
	})
	req := httptest.NewRequest(http.MethodGet, "/search?q=kubernetes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Kubernetes networking") {
		t.Fatalf("missing match:\n%s", body)
	}
	if strings.Contains(body, "Go modules") {
		t.Fatalf("non-match leaked:\n%s", body)
	}
	if !strings.Contains(body, "1 match") {
		t.Fatalf("missing count header:\n%s", body)
	}
}

func TestSearchBlankQueryHasNoInstructions(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Type a query") {
		t.Fatalf("search page still shows instructions:\n%s", body)
	}
	if !strings.Contains(body, `name="q"`) {
		t.Fatalf("search form missing:\n%s", body)
	}
}

func TestSearchNoMatchesShowsEmptyState(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "Kubernetes networking", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
	})
	req := httptest.NewRequest(http.MethodGet, "/search?q=zzznomatch", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "0 matches") || !strings.Contains(body, "Nothing matches your search") {
		t.Fatalf("missing zero-result feedback:\n%s", body)
	}
	if strings.Contains(body, "Kubernetes networking") {
		t.Fatalf("non-match leaked into zero-result page:\n%s", body)
	}
}

func TestEntryDetailFallsBackToSummaryWhenContentEmpty(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	// Atom <summary>-only feeds (e.g. Simon Willison) put the body in Summary, not Content.
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P",
		Content: "", Summary: "<p>full body from summary</p>",
		Status: core.StatusUnread, PublishedAt: time.Unix(100, 0),
	}})

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "full body from summary") {
		t.Fatalf("detail view did not fall back to summary:\n%s", rec.Body.String())
	}
}

func TestEntryDetailPrefersContentOverSummary(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P",
		Content: "<p>full content</p>", Summary: "<p>short summary</p>",
		Status: core.StatusUnread, PublishedAt: time.Unix(100, 0),
	}})

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "full content") {
		t.Fatalf("detail view missing content:\n%s", body)
	}
	if strings.Contains(body, "short summary") {
		t.Fatalf("detail view should not render summary when content present:\n%s", body)
	}
}

func TestSearchCapsHeaderAtFifty(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	entries := make([]*core.Entry, 0, 55)
	for i := 0; i < 55; i++ {
		entries = append(entries, &core.Entry{
			UserID: core.DefaultUserID, FeedID: fid, GUID: "g" + strconv.Itoa(i),
			Title: "post number " + strconv.Itoa(i), Status: core.StatusUnread, PublishedAt: time.Unix(int64(100+i), 0),
		})
	}
	store.UpsertEntries(ctx, fid, entries)
	req := httptest.NewRequest(http.MethodGet, "/search?q=post", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "top 50 matches") {
		t.Fatalf("capped header missing:\n%s", rec.Body.String())
	}
}

func TestSubscribeFullContentCheckboxAndToggle(t *testing.T) {
	// Build the handler with stubs that make Subscribe succeed.
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, Body: []byte("<rss/>"), ETag: `"e"`}}
	parser := coretest.StubParser{PF: &core.ParsedFeed{Title: "Blog", SiteURL: "https://x.example/"}}
	fs := core.NewFeedService(store, fetcher, parser, coretest.PassSanitizer{}, coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	srv := web.New(fs, es, cs, ss, log, nil, nil, 20)

	// Subscribe with full_content checkbox checked.
	form := url.Values{"url": {"https://x.example/feed"}, "full_content": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("subscribe status %d, body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("subscribe missing HX-Refresh: true; got %q", rec.Header().Get("HX-Refresh"))
	}

	feeds, _ := store.ListFeeds(context.Background(), core.DefaultUserID)
	if len(feeds) != 1 || !feeds[0].FetchFullContent {
		t.Fatalf("subscribe did not set FetchFullContent: %+v", feeds)
	}

	// Toggle off.
	id := feeds[0].ID
	off := url.Values{"full_content": {"off"}}
	req = httptest.NewRequest(http.MethodPost, "/feeds/"+strconv.FormatInt(int64(id), 10)+"/full-content", strings.NewReader(off.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("toggle status %d, body: %s", rec.Code, rec.Body.String())
	}

	feeds, _ = store.ListFeeds(context.Background(), core.DefaultUserID)
	if feeds[0].FetchFullContent {
		t.Fatalf("toggle did not clear FetchFullContent")
	}
}

func TestMarkFeedReadRefreshesAndMarks(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "a", Title: "A", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "b", Title: "B", Status: core.StatusUnread, PublishedAt: time.Unix(200, 0)},
	})

	req := httptest.NewRequest(http.MethodPost, "/feeds/"+strconv.FormatInt(int64(fid), 10)+"/mark-read", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rec.Code)
	}
	if rec.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("missing HX-Refresh: true header; got %q", rec.Header().Get("HX-Refresh"))
	}
	entries, _, _ := store.ListEntries(ctx, core.DefaultUserID, core.EntryFilter{FeedID: &fid})
	for _, e := range entries {
		if e.Status != core.StatusRead {
			t.Fatalf("entry %d still unread after mark-read", e.ID)
		}
	}
}

func TestFeedPageShowsMarkAllReadButton(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "a", Title: "A", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(int64(fid), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	want := "/feeds/" + strconv.FormatInt(int64(fid), 10) + "/mark-read"
	if !strings.Contains(body, want) || !strings.Contains(body, "Mark all read") {
		t.Fatalf("feed page missing mark-all-read button:\n%s", body)
	}

	// The home/unread view must NOT show it (button is feed-view only this iteration).
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if strings.Contains(rec2.Body.String(), "Mark all read") {
		t.Fatal("home view should not show the mark-all-read button yet")
	}
}

func TestListRendersDateTooltip(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "Hello", Status: core.StatusUnread, PublishedAt: time.Unix(1_600_000_000, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	// Assert the <time> element itself carries both datetime and title — checking
	// the two attributes independently is too weak (title=" also appears on the
	// action buttons), so a missing tooltip on <time> could slip through.
	if !regexp.MustCompile(`<time datetime="[^"]+" title="[^"]+">`).MatchString(body) {
		t.Fatalf("list row <time> missing datetime+title tooltip:\n%s", body)
	}
}

func TestEmptyUnreadShowsCaughtUp(t *testing.T) {
	h, _ := newWeb(t) // no feeds, no entries
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "empty-state") || !strings.Contains(body, "caught up") {
		t.Fatalf("empty unread view missing caught-up empty state:\n%s", body)
	}
}

func TestEmptyStateAbsentWhenEntriesExist(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "Hi", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "empty-state") {
		t.Fatalf("empty state shown despite an entry being present")
	}
}

func TestFeedsPageShowsCounts(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "A", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g2", Title: "B", Status: core.StatusUnread, PublishedAt: time.Unix(101, 0)},
	})

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	// The feedrow template renders: <span class="unread">N unread</span> / N
	// so "unread" appears as part of the meta line; total count follows the slash.
	if !strings.Contains(body, "2 unread") {
		t.Fatalf("feeds page missing unread count:\n%s", body)
	}
	if !strings.Contains(body, "/ 2") {
		t.Fatalf("feeds page missing total count (shown as '/ N'):\n%s", body)
	}
}

func TestUnreadViewShowsTotalCount(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "A", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)},
		{UserID: core.DefaultUserID, FeedID: fid, GUID: "g2", Title: "B", Status: core.StatusUnread, PublishedAt: time.Unix(101, 0)},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "2 unread") {
		t.Fatalf("unread view missing total count:\n%s", rec.Body.String())
	}
}

func TestSingleFeedViewShowsCount(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g1", Title: "A", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0)}})
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(int64(fid), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "1 unread · 1 total") {
		t.Fatalf("single-feed view missing count:\n%s", rec.Body.String())
	}
}

func TestReaderRewritesImagesWhenProxyOn(t *testing.T) {
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	rewrite := func(u string) string { return "/img?u=" + u }
	h := web.New(fs, es, cs, ss, log, nil, rewrite, 20)

	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0), Content: `<p>x <img src="https://o.test/p.jpg"> y</p>`}})

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "/img?u=https://o.test/p.jpg") {
		t.Fatalf("image not proxied:\n%s", body)
	}
	if strings.Contains(body, `src="https://o.test/p.jpg"`) {
		t.Fatalf("origin src still present:\n%s", body)
	}
}

func TestReaderKeepsOriginWhenProxyOff(t *testing.T) {
	h, store := newWeb(t) // nil rewrite
	ctx := context.Background()
	fid, _ := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/f", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	ins, _ := store.UpsertEntries(ctx, fid, []*core.Entry{{UserID: core.DefaultUserID, FeedID: fid, GUID: "g", Title: "P", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0), Content: `<p><img src="https://o.test/p.jpg"></p>`}})

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "https://o.test/p.jpg") {
		t.Fatalf("origin src should be untouched when proxy off:\n%s", rec.Body.String())
	}
}

func TestFeedsPageShowsStalledBadge(t *testing.T) {
	store := coretest.NewMemStore()
	log := coretest.DiscardLogger()
	fs := core.NewFeedService(store, coretest.StubFetcher{}, coretest.StubParser{}, coretest.PassSanitizer{}, coretest.StubClock{}, log,
		core.FeedServiceConfig{Reschedule: core.RescheduleConfig{Interval: time.Minute, MaxBackoff: time.Hour}, Jitter: func(time.Duration) time.Duration { return 0 }})
	es := core.NewEntryService(store, log)
	cs := core.NewCategoryService(store, log)
	ss := core.NewSearchService(store, log)
	h := web.New(fs, es, cs, ss, log, nil, nil, 3) // error limit = 3

	ctx := context.Background()
	now := time.Unix(1, 0)
	// stalled: error_count >= limit
	store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/dead", Title: "Dead", ErrorCount: 5, LastError: "boom", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})
	// healthy: under the limit
	store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/ok", Title: "OK", ErrorCount: 1, LastError: "blip", NextCheckAt: now, CreatedAt: now, UpdatedAt: now})

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	// feedrow template emits <span class="stalled">⚠ stalled — ...</span>
	if !strings.Contains(body, `class="stalled"`) || !strings.Contains(body, "⚠ stalled") {
		t.Fatalf("stalled feed missing badge:\n%s", body)
	}
	// the healthy feed (error_count 1 < 3) shows the inline error, not the stalled badge
	if strings.Count(body, `class="stalled"`) != 1 {
		t.Fatalf("expected exactly one stalled element, body:\n%s", body)
	}
	// feedrow emits <span class="err">error: ...</span> for non-stalled errors
	if !strings.Contains(body, "error: blip") {
		t.Fatalf("healthy feed should show inline error, not badge:\n%s", body)
	}
}
