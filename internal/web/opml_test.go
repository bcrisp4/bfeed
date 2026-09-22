package web_test

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestExportOPMLDownload(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	catID, err := store.CreateCategory(ctx, &core.Category{UserID: core.DefaultUserID, Title: "My feeds"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a.test/feed", Title: "A", CategoryID: &catID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFeed(ctx, &core.Feed{UserID: 2, FeedURL: "https://other.test/feed", Title: "Other user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCategory(ctx, &core.Category{UserID: 2, Title: "Private category"}); err != nil {
		t.Fatal(err)
	}

	page := do(t, h, http.MethodGet, "/feeds")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `href="/feeds/export"`) {
		t.Fatalf("feeds page lacks export link: code=%d body=%s", page.Code, page.Body.String())
	}
	rec := do(t, h, http.MethodGet, "/feeds/export")
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/x-opml; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="bfeed-feeds.opml"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	var doc struct {
		Body struct {
			Outlines []struct {
				Text     string `xml:"text,attr"`
				Outlines []struct {
					XMLURL string `xml:"xmlUrl,attr"`
				} `xml:"outline"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid OPML: %v\n%s", err, rec.Body.String())
	}
	if len(doc.Body.Outlines) != 1 || doc.Body.Outlines[0].Text != "My feeds" || len(doc.Body.Outlines[0].Outlines) != 1 || doc.Body.Outlines[0].Outlines[0].XMLURL != "https://a.test/feed" {
		t.Fatalf("wrong download content: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "other.test") || strings.Contains(rec.Body.String(), "Private category") {
		t.Fatalf("download includes another user's subscriptions: %s", rec.Body.String())
	}
}

func TestExportOPMLEmptyLibrary(t *testing.T) {
	h, _ := newWeb(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds/export", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<opml version="2.0">`) {
		t.Fatalf("empty export code=%d body=%s", rec.Code, rec.Body.String())
	}
}
