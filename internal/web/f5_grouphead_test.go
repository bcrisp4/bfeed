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
)

// F5: renderFeedRow now reads one feed's stats for the row, but its completion-tick
// OOB group head must still sum unread across EVERY feed in the category — not just
// the polled feed. Regression guard for the single-feed-stats swap.
func TestFeedRowGroupHeadSumsWholeCategory(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, _ := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "News"})
	mk := func(url string) core.ID {
		id, _ := store.CreateFeed(ctx, &core.Feed{
			UserID: core.DefaultUserID, FeedURL: url, Title: url, CategoryID: &catID,
			NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
		})
		return id
	}
	fa := mk("https://a.test/f")
	fb := mk("https://b.test/f")
	seed := func(fid core.ID, guid string) {
		coretest.SeedEntry(store, &core.Entry{
			UserID: core.DefaultUserID, FeedID: fid, GUID: guid, Title: guid,
			Status: core.StatusUnread, PublishedAt: time.Unix(100, 0),
		})
	}
	seed(fa, "a1")
	seed(fb, "b1")
	seed(fb, "b2") // group total unread = 3 (1 in A, 2 in B)

	// Poll feed A's row (not refreshing → completion branch emits the OOB group head).
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(int64(fa), 10)+"/row", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("row status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hx-swap-oob") {
		t.Fatalf("no OOB group head emitted:\n%s", body)
	}
	if !strings.Contains(body, "3 unread") {
		t.Fatalf("group head did not sum whole category (want 3 unread):\n%s", body)
	}
}
