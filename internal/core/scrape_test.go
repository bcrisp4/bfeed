package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/core/coretest"
)

func newScrapeFixture(t *testing.T, fetch core.Fetcher, ext core.Extractor) (*core.ScrapeService, *coretest.MemStore, *coretest.StubClock) {
	t.Helper()
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	svc := core.NewScrapeService(store, fetch, ext, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)
	return svc, store, clk
}

func TestScrapeEntrySuccessWritesContentAndMarksDone(t *testing.T) {
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html; charset=utf-8", Body: []byte("<html>..</html>")}}
	ext := coretest.StubExtractor{HTML: "<p>extracted</p>"}
	svc, store, _ := newScrapeFixture(t, fetch, ext)
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	if err := svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending}); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.Content != "<p>extracted</p>" || got.ExtractState != core.ExtractDone {
		t.Fatalf("got %q %q", got.Content, got.ExtractState)
	}
}

// recordingExtractor / recordingSanitizer capture the base URL passed to them so
// the test can assert the post-redirect FinalURL (not the pre-redirect entry URL)
// is used to absolutize relative links.
type recordingExtractor struct {
	html    string
	pageURL string
}

func (e *recordingExtractor) Extract(_ context.Context, pageURL string, _ []byte) (string, error) {
	e.pageURL = pageURL
	return e.html, nil
}

type recordingSanitizer struct{ baseURL string }

func (s *recordingSanitizer) Sanitize(html, baseURL string) string {
	s.baseURL = baseURL
	return html
}

func TestScrapeEntryUsesFinalURLAsBase(t *testing.T) {
	const entryURL = "https://feedproxy.example/redir"
	const finalURL = "https://real.example.com/article"
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{
		Status: 200, ContentType: "text/html", Body: []byte("<html>..</html>"), FinalURL: finalURL,
	}}
	ext := &recordingExtractor{html: "<p>x</p>"}
	san := &recordingSanitizer{}
	store := coretest.NewMemStore()
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	svc := core.NewScrapeService(store, fetch, ext, san, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: time.Minute, MaxBackoff: time.Hour}, nil)
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: entryURL, ExtractState: core.ExtractPending})
	if err := svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: entryURL, ExtractState: core.ExtractPending}); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}
	if ext.pageURL != finalURL {
		t.Errorf("Extract base = %q, want final URL %q", ext.pageURL, finalURL)
	}
	if san.baseURL != finalURL {
		t.Errorf("Sanitize base = %q, want final URL %q", san.baseURL, finalURL)
	}
}

func TestScrapeEntryRetriesThenFails(t *testing.T) {
	// A genuine content failure (404: page gone / non-feed) burns attempts and goes
	// terminal at the cap. Contrast the transient 429/5xx path below (audit B10 #1).
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 404, ContentType: "text/html"}}
	svc, store, _ := newScrapeFixture(t, fetch, coretest.StubExtractor{})
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	e := &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 0}
	// attempt 1 → still pending, attempt count 1
	_ = svc.ScrapeEntry(context.Background(), e)
	got, _ := store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.ExtractState != core.ExtractPending || got.ExtractAttempts != 1 {
		t.Fatalf("after 1 fail: %q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
	// drive to the cap
	_ = svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 1})
	_ = svc.ScrapeEntry(context.Background(), &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 2})
	got, _ = store.GetEntry(context.Background(), core.DefaultUserID, id)
	if got.ExtractState != core.ExtractFailed {
		t.Fatalf("want failed at cap, got %q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
	// The terminal failure reason is persisted (status + content-type), not discarded.
	if got.ExtractError == "" {
		t.Fatalf("want persisted extract_error, got empty")
	}
}

// B10 #1: a transient 429/5xx must NOT burn an attempt — otherwise a full-content
// backfill burst that trips a rate limit converts a whole feed's backlog to
// terminal failures. It reschedules honouring Retry-After instead.
func TestScrapeTransientStatusDoesNotBurnAttempts(t *testing.T) {
	ctx := context.Background()
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 429, ContentType: "text/html", RetryAfter: 2 * time.Hour}}
	svc, store, clk := newScrapeFixture(t, fetch, coretest.StubExtractor{})
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})

	// Even many attempts against a rate-limited host never go terminal.
	for i := 0; i < 5; i++ {
		if err := svc.ScrapeEntry(ctx, &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending, ExtractAttempts: 0}); err != nil {
			t.Fatalf("ScrapeEntry: %v", err)
		}
	}
	got, _ := store.GetEntry(ctx, core.DefaultUserID, id)
	if got.ExtractState != core.ExtractPending || got.ExtractAttempts != 0 {
		t.Fatalf("transient 429: want pending attempts=0, got %q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
	// Retry-After (2h) beats BaseBackoff (10m): not due at +1h, due at +3h.
	if due, _ := store.ListPendingExtractions(ctx, clk.T.Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("entry due before Retry-After elapsed: %d", len(due))
	}
	if due, _ := store.ListPendingExtractions(ctx, clk.T.Add(3*time.Hour), 10); len(due) != 1 {
		t.Fatalf("entry not due after Retry-After: %d", len(due))
	}
}

// B10 review: a hostile/misconfigured Retry-After must be clamped to MaxBackoff so
// a transient failure can't park an entry for years.
func TestScrapeRetryAfterClampedToMaxBackoff(t *testing.T) {
	ctx := context.Background()
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 503, ContentType: "text/html", RetryAfter: 100 * 24 * time.Hour}}
	svc, store, clk := newScrapeFixture(t, fetch, coretest.StubExtractor{}) // MaxBackoff 24h
	id := coretest.SeedEntry(store, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	if err := svc.ScrapeEntry(ctx, &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending}); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}
	// Clamped to MaxBackoff (24h): due just after 24h, not 100 days out.
	if due, _ := store.ListPendingExtractions(ctx, clk.T.Add(25*time.Hour), 10); len(due) != 1 {
		t.Fatalf("entry not due after MaxBackoff clamp: %d", len(due))
	}
}

// B4/F3: a persist (SetEntryContent) failure must reschedule the entry with
// backoff (via fail), not return raw leaving it pending with next_extract_at in
// the past and attempts unincremented — otherwise the Scraper retries every tick.
func TestScrapeReschedulesWhenPersistFails(t *testing.T) {
	ctx := context.Background()
	inner := coretest.NewMemStore()
	store := setEntryContentErrStore{inner}
	clk := &coretest.StubClock{T: time.Unix(1_700_000_000, 0).UTC()}
	fetch := coretest.StubFetcher{Resp: &core.FetchResponse{Status: 200, ContentType: "text/html; charset=utf-8", Body: []byte("<html>..</html>")}}
	ext := coretest.StubExtractor{HTML: "<p>extracted</p>"}
	svc := core.NewScrapeService(store, fetch, ext, coretest.PassSanitizer{}, clk, coretest.DiscardLogger(),
		core.ScrapeConfig{MaxAttempts: 3, BaseBackoff: 10 * time.Minute, MaxBackoff: 24 * time.Hour}, nil)

	id := coretest.SeedEntry(inner, &core.Entry{UserID: core.DefaultUserID, FeedID: 1, GUID: "g", URL: "https://x/a", ExtractState: core.ExtractPending})
	if err := svc.ScrapeEntry(ctx, &core.Entry{ID: id, URL: "https://x/a", ExtractState: core.ExtractPending}); err != nil {
		t.Fatalf("ScrapeEntry: %v", err)
	}
	got, _ := inner.GetEntry(ctx, core.DefaultUserID, id)
	if got.ExtractState != core.ExtractPending || got.ExtractAttempts != 1 {
		t.Fatalf("persist failure not rescheduled: state=%q attempts=%d", got.ExtractState, got.ExtractAttempts)
	}
}

// setEntryContentErrStore wraps MemStore and fails every SetEntryContent write,
// simulating a store-layer persist failure during extraction.
type setEntryContentErrStore struct{ *coretest.MemStore }

func (setEntryContentErrStore) SetEntryContent(context.Context, core.ID, string) error {
	return errors.New("disk full")
}

func TestExtractBackoffGrowsAndCaps(t *testing.T) {
	cfg := core.ScrapeConfig{BaseBackoff: 10 * time.Minute, MaxBackoff: time.Hour}
	b1 := core.ExtractBackoff(cfg, 1, nil)
	b2 := core.ExtractBackoff(cfg, 2, nil)
	b9 := core.ExtractBackoff(cfg, 9, nil)
	if b1 != 10*time.Minute || b2 != 20*time.Minute || b9 != time.Hour {
		t.Fatalf("backoff: %v %v %v", b1, b2, b9)
	}
}
