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

// Issue #92: entries carrying a per-item discussion URL (RSS <comments>) show a
// quiet external "comments" link, but only when it exists and differs from the
// entry URL.

func seedCommentsEntry(t *testing.T, store *coretest.MemStore, guid, url, commentsURL string) core.ID {
	t.Helper()
	ctx := context.Background()
	fid, err := store.CreateFeed(ctx, &core.Feed{
		UserID: core.DefaultUserID, FeedURL: "https://b.test/" + guid, Title: "Blog",
		NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return coretest.SeedEntry(store, &core.Entry{
		UserID: core.DefaultUserID, FeedID: fid, GUID: guid, URL: url, Title: "P-" + guid,
		Content: "<p>body</p>", Status: core.StatusUnread, PublishedAt: time.Unix(100, 0),
		CommentsURL: commentsURL,
	})
}

func TestReaderShowsCommentsLink(t *testing.T) {
	h, store := newWeb(t)
	id := seedCommentsEntry(t, store, "g1", "https://x.test/a", "https://news.test/item?id=1")

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(id), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("reader status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="https://news.test/item?id=1"`) {
		t.Fatalf("reader missing comments href:\n%s", body)
	}
	if !strings.Contains(body, "Comments") {
		t.Fatalf("reader missing comments label:\n%s", body)
	}
}

func TestReaderHidesCommentsLinkWhenAbsentOrSameAsURL(t *testing.T) {
	cases := []struct {
		name, commentsURL string
	}{
		{"absent", ""},
		{"same as entry URL", "https://x.test/a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newWeb(t)
			id := seedCommentsEntry(t, store, "g1", "https://x.test/a", tc.commentsURL)

			req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(id), 10), nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("reader status %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "Comments") {
				t.Fatalf("reader shows comments link for %s:\n%s", tc.name, rec.Body.String())
			}
		})
	}
}

func TestListRowShowsCommentsLink(t *testing.T) {
	h, store := newWeb(t)
	seedCommentsEntry(t, store, "g1", "https://x.test/a", "https://news.test/item?id=1")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="comments" href="https://news.test/item?id=1"`) {
		t.Fatalf("list row missing comments link:\n%s", body)
	}
}

func TestListRowHidesCommentsLinkWhenAbsentOrSameAsURL(t *testing.T) {
	cases := []struct {
		name, commentsURL string
	}{
		{"absent", ""},
		{"same as entry URL", "https://x.test/a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newWeb(t)
			seedCommentsEntry(t, store, "g1", "https://x.test/a", tc.commentsURL)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("list status %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), `class="comments"`) {
				t.Fatalf("list row shows comments link for %s:\n%s", tc.name, rec.Body.String())
			}
		})
	}
}
