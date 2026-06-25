package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func TestEditFormRendersPanel(t *testing.T) {
	h, st := newTestHandler(t, coretest.StubFetcher{})
	id := seedFeed(t, st)
	rec := do(t, h, "GET", "/feeds/"+itoa(id)+"/edit")
	body := rec.Body.String()
	if !strings.Contains(body, `class="feed-edit"`) || !strings.Contains(body, `name="url"`) {
		t.Errorf("edit panel missing, body=%s", body)
	}
}

func TestEditSaveRenamesAndSwapsRow(t *testing.T) {
	h, st := newTestHandler(t, coretest.StubFetcher{})
	id := seedFeed(t, st)
	// Use the same feed URL as seedFeed so URLChanged=false; category unchanged
	// (empty = uncategorised = same as seed). Only the title changes.
	form := strings.NewReader("title=My+Name&url=https%3A%2F%2Fexample.com%2Ffeed.xml&category_id=")
	req := httptest.NewRequest("POST", "/feeds/"+itoa(id), form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("HX-Refresh") != "" {
		t.Error("same-category rename should swap the row, not HX-Refresh")
	}
	if !strings.Contains(rec.Body.String(), "My Name") {
		t.Errorf("renamed row not returned, body=%s", rec.Body.String())
	}
	f, _ := st.GetFeed(context.Background(), core.DefaultUserID, id)
	if f.UserTitle != "My Name" {
		t.Errorf("user_title=%q", f.UserTitle)
	}
}

func TestEditSaveCategoryChangedReturnsHXRefresh(t *testing.T) {
	h, st := newTestHandler(t, coretest.StubFetcher{})
	id := seedFeed(t, st)
	// Create a category to assign.
	catID, err := st.CreateCategory(context.Background(), &core.Category{
		UserID: core.DefaultUserID,
		Title:  "Tech",
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	form := strings.NewReader("title=&url=https%3A%2F%2Fexample.com%2Ffeed.xml&category_id=" + itoa(catID))
	req := httptest.NewRequest("POST", "/feeds/"+itoa(id), form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("HX-Refresh") != "true" {
		t.Errorf("category change should return HX-Refresh: true, got %q", rec.Header().Get("HX-Refresh"))
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestEditFormReturns404ForUnknownFeed(t *testing.T) {
	h, _ := newTestHandler(t, coretest.StubFetcher{})
	rec := do(t, h, "GET", "/feeds/99999/edit")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestEditSaveBadURLReturns422WithPanel(t *testing.T) {
	h, st := newTestHandler(t, coretest.StubFetcher{})
	id := seedFeed(t, st)
	form := strings.NewReader("title=&url=javascript%3Aalert(1)&category_id=")
	req := httptest.NewRequest("POST", "/feeds/"+itoa(id), form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="feed-edit"`) {
		t.Errorf("422 response should re-render the edit panel, body=%s", body)
	}
	if !strings.Contains(body, `class="form-error"`) {
		t.Errorf("422 response should show form-error, body=%s", body)
	}
}

// TestEditSaveURLChangedSwapsRefreshingRow verifies that when the feed URL is
// changed, editFeed starts a background refresh and returns the self-polling
// refreshing row fragment (not an HX-Refresh full reload).
func TestEditSaveURLChangedSwapsRefreshingRow(t *testing.T) {
	// Use a fetcher that returns a non-nil NotModified response so the background
	// goroutine (startRefresh → feeds.Refresh) doesn't nil-deref on a zero resp.
	fetcher := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 304, NotModified: true}}
	h, st := newTestHandler(t, fetcher)
	id := seedFeed(t, st)
	// Post a URL different from the seeded "https://example.com/feed.xml".
	form := strings.NewReader("title=&url=https%3A%2F%2Fother.example.com%2Ffeed.xml&category_id=")
	req := httptest.NewRequest("POST", "/feeds/"+itoa(id), form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// URL change must swap the row, not trigger a full reload.
	if hdr := rec.Header().Get("HX-Refresh"); hdr != "" {
		t.Errorf("URL change should swap the row, not HX-Refresh; got HX-Refresh=%q", hdr)
	}
	// The handler calls startRefresh before renderFeedRow; busy tracker marks the
	// feed in-flight so the rendered row is in the "refreshing" state.
	body := rec.Body.String()
	if !strings.Contains(body, `hx-trigger="every 1500ms"`) {
		t.Errorf("URLChanged row should be in refreshing state (self-polling), body=%s", body)
	}
}
