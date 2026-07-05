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
)

// GET /entries/{id} promotes an image-only <p> to <figure> in the rendered
// reader HTML (#90) — and does so with the image proxy disabled (newWeb wires
// a nil imgRewrite), because promotion must not depend on proxy config.
func TestReaderPromotesBareParagraphImage(t *testing.T) {
	h, store := newWeb(t)
	ctx := context.Background()
	fid, err := store.CreateFeed(ctx, &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://b.test/promote", Title: "Blog", NextCheckAt: time.Unix(1, 0), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.UpsertEntries(ctx, fid, []*core.Entry{{
		UserID: core.DefaultUserID, FeedID: fid, GUID: "g-promote", Title: "Chart post",
		Content: `<p><img src="https://o/chart.png" alt="chart"></p><p>Real prose stays a paragraph.</p>`,
		Status:  core.StatusUnread, PublishedAt: time.Unix(100, 0),
	}})
	if err != nil || len(ins) != 1 {
		t.Fatalf("seed entry: ins=%d err=%v", len(ins), err)
	}

	req := httptest.NewRequest(http.MethodGet, "/entries/"+strconv.FormatInt(int64(ins[0].ID), 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<figure><img src="https://o/chart.png"`) {
		t.Fatalf("bare <p><img> not promoted to <figure> in reader output:\n%s", body)
	}
	if !strings.Contains(body, "<p>Real prose stays a paragraph.</p>") {
		t.Fatalf("prose paragraph mangled:\n%s", body)
	}
}
