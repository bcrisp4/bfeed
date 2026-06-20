package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	return web.New(fs, es, cs, log), store
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
