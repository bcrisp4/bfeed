package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	return web.New(fs, es, cs, ss, log), store
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
	if rec.Code != 200 || !strings.Contains(body, `<h2 class="cat-heading">News</h2>`) || !strings.Contains(body, `<h2 class="cat-heading">Uncategorised</h2>`) {
		t.Fatalf("feeds page missing category headings: code=%d\n%s", rec.Code, body)
	}
	if !strings.Contains(body, "InCat") || !strings.Contains(body, "NoCat") {
		t.Fatalf("feeds page missing feeds:\n%s", body)
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
	req := httptest.NewRequest(http.MethodGet, "/", nil)
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

func TestSearchBlankQueryShowsPrompt(t *testing.T) {
	h, _ := newWeb(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Type a query") {
		t.Fatalf("blank-query prompt missing, code=%d body=%s", rec.Code, rec.Body.String())
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
	if !strings.Contains(body, "0 matches") || !strings.Contains(body, "No entries match") {
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
	srv := web.New(fs, es, cs, ss, log)

	// Subscribe with full_content checkbox checked.
	form := url.Values{"url": {"https://x.example/feed"}, "full_content": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("subscribe status %d, body: %s", rec.Code, rec.Body.String())
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
