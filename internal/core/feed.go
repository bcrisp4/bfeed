package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

type FeedServiceConfig struct {
	Schedule   ScheduleConfig
	Reschedule RescheduleConfig
	Jitter     func(time.Duration) time.Duration
}

type FeedService struct {
	store   Store
	fetcher Fetcher
	parser  FeedParser
	san     Sanitizer
	clk     Clock
	log     *slog.Logger
	cfg     FeedServiceConfig
}

func NewFeedService(store Store, fetcher Fetcher, parser FeedParser, san Sanitizer, clk Clock, log *slog.Logger, cfg FeedServiceConfig) *FeedService {
	// Normalize so a zero-value config can never schedule now+0 (a tight re-poll
	// loop). Prod passes validated values; this guards tests/other callers.
	if cfg.Schedule.MinInterval <= 0 {
		cfg.Schedule.MinInterval = 5 * time.Minute
	}
	if cfg.Schedule.MaxInterval <= cfg.Schedule.MinInterval {
		cfg.Schedule.MaxInterval = 24 * time.Hour
	}
	if cfg.Schedule.Factor <= 0 {
		cfg.Schedule.Factor = 1
	}
	if cfg.Reschedule.Interval <= 0 {
		cfg.Reschedule.Interval = cfg.Schedule.MinInterval
	}
	if cfg.Reschedule.MaxBackoff <= 0 {
		cfg.Reschedule.MaxBackoff = cfg.Schedule.MaxInterval
	}
	return &FeedService{store: store, fetcher: fetcher, parser: parser, san: san, clk: clk, log: log, cfg: cfg}
}

var _ FeedPoller = (*FeedService)(nil)

func (s *FeedService) Get(ctx context.Context, userID, feedID ID) (*Feed, error) {
	return s.store.GetFeed(ctx, userID, feedID)
}

func (s *FeedService) List(ctx context.Context, userID ID) ([]*Feed, error) {
	return s.store.ListFeeds(ctx, userID)
}

func (s *FeedService) Delete(ctx context.Context, userID, feedID ID) error {
	return s.store.DeleteFeed(ctx, userID, feedID)
}

// SetFullContent toggles per-feed full-content extraction for an owned feed. The
// flag flip and backlog reconciliation (enable → backfill to pending; disable →
// cancel queued) happen atomically in the store, so a partial failure can never
// leave the flag on with an unqueued backlog (audit B10).
func (s *FeedService) SetFullContent(ctx context.Context, userID, feedID ID, on bool) error {
	return s.store.SetFeedFullContent(ctx, userID, feedID, on, s.clk.Now())
}

// ensureCategoryOwned validates that a non-nil categoryID exists for userID.
// An absent/foreign category is a client error (ErrValidation); any other store
// error (DB unavailable, context cancelled) is propagated as-is so transient
// failures are not misreported as validation failures.
func (s *FeedService) ensureCategoryOwned(ctx context.Context, userID ID, categoryID *ID) error {
	if categoryID == nil {
		return nil
	}
	if _, err := s.store.GetCategory(ctx, userID, *categoryID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: unknown category", ErrValidation)
		}
		return err
	}
	return nil
}

// CreateSubscription validates the URL and category and persists a pending feed
// row (no network I/O). The feed has CheckedAt == nil and a URL-fallback Title
// until ResolveAndIngest runs. Returns the feed with ID set.
func (s *FeedService) CreateSubscription(ctx context.Context, userID ID, rawURL string, categoryID *ID, fetchFullContent bool) (*Feed, error) {
	rawURL = normalizeFeedURL(rawURL)
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%w: invalid feed URL", ErrValidation)
	}
	if err := s.ensureCategoryOwned(ctx, userID, categoryID); err != nil {
		return nil, err
	}
	now := s.clk.Now()
	// Grace period before the pending row is poll-due: the web layer resolves it in a
	// background goroutine right after this returns, and that resolve owns the first
	// ingest. Making it immediately due (NextCheckAt: now) lets the Poller dispatch it
	// concurrently and clobber the resolve's result with a last-write-wins UpdateFeed
	// (audit B7). One MinInterval out means the Poller only picks it up if the resolve
	// died; a normal resolve overwrites next_check_at via recordSuccess well before.
	f := &Feed{
		UserID: userID, CategoryID: categoryID, FeedURL: rawURL, Title: feedTitle("", rawURL),
		NextCheckAt: now.Add(s.cfg.Schedule.MinInterval), CreatedAt: now, UpdatedAt: now,
		FetchFullContent: fetchFullContent,
	}
	id, err := s.store.CreateFeed(ctx, f)
	if err != nil {
		return nil, err
	}
	f.ID = id
	return f, nil
}

// ResolveAndIngest fetches and parses the feed (with HTML discovery), persists a
// discovered/changed URL, ingests entries, and reschedules. On any failure it
// records the error on the feed (so a pending row becomes an error row) and
// returns it. Intended to run in a background goroutine.
func (s *FeedService) ResolveAndIngest(ctx context.Context, f *Feed) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = s.recordPanic(ctx, f, r)
		}
	}()
	feedURL, pf, resp, err := s.resolveFeed(ctx, f.FeedURL)
	if err != nil {
		return s.recordError(ctx, f, s.clk.Now(), err.Error(), 0)
	}
	if feedURL != f.FeedURL {
		if err := s.store.SetFeedURL(ctx, f.UserID, f.ID, feedURL, s.clk.Now()); err != nil {
			if errors.Is(err, ErrConflict) {
				// The resolved URL is already subscribed under another row, so this
				// just-created row is a definitional duplicate: delete it rather than
				// keep an error row that re-polls the (non-feed) subscribe URL forever
				// with no discovery step to ever self-heal (audit B8). No tombstones
				// are written — a re-subscribe gets a fresh feed_id.
				if derr := s.store.DeleteFeed(ctx, f.UserID, f.ID); derr != nil {
					// Deletion failed, so the row still exists: degrade to the
					// error-row behaviour (informative message + backoff) rather
					// than return nil and leave it silently due, re-polling the
					// non-feed URL forever.
					s.log.Warn("deleting duplicate feed row after URL conflict failed",
						"feed_id", int64(f.ID), "url", feedURL, "error", derr)
					return s.recordError(ctx, f, s.clk.Now(), "already subscribed to this feed", 0)
				}
				return nil
			}
			return s.recordError(ctx, f, s.clk.Now(), err.Error(), 0)
		}
		f.FeedURL = feedURL
	}
	return s.recordSuccess(ctx, f, s.clk.Now(), resp, pf)
}

// Subscribe is the synchronous convenience used by non-web callers and tests:
// create the row, then resolve+ingest inline. A fetch/parse failure is recorded
// on the feed by ResolveAndIngest (which returns nil), so the row is kept in an
// error state — matching the web path, which calls CreateSubscription +
// ResolveAndIngest directly. The rollback below only fires on a hard store error
// (ResolveAndIngest returning non-nil), where the half-written row is unusable.
func (s *FeedService) Subscribe(ctx context.Context, userID ID, rawURL string, categoryID *ID, fetchFullContent bool) (*Feed, error) {
	f, err := s.CreateSubscription(ctx, userID, rawURL, categoryID, fetchFullContent)
	if err != nil {
		return nil, err
	}
	if err := s.ResolveAndIngest(ctx, f); err != nil {
		_ = s.store.DeleteFeed(ctx, userID, f.ID)
		return nil, err
	}
	return f, nil
}

// resolveFeed fetches rawURL; if it parses as a feed, use it. Otherwise try HTML
// discovery and fetch the first discovered feed URL.
func (s *FeedService) resolveFeed(ctx context.Context, rawURL string) (string, *ParsedFeed, *FetchResponse, error) {
	resp, err := s.fetcher.Fetch(ctx, FetchRequest{URL: rawURL})
	if err != nil {
		return "", nil, nil, fmt.Errorf("%w: fetch failed: %w", ErrValidation, err)
	}
	if resp.Status != 200 || len(resp.Body) == 0 {
		return "", nil, nil, fmt.Errorf("%w: feed returned status %d", ErrValidation, resp.Status)
	}
	// gofeed returns a non-nil error for non-feed content (HTML, arbitrary XML),
	// so a nil error with a non-nil feed already means "positively identified as a
	// feed" — do NOT also require a title or entries, or a valid but freshly
	// created (untitled, item-less) feed is wrongly rejected and falls through to
	// HTML discovery of its own non-HTML body.
	// Resolve relative links against the URL the bytes actually came from (after
	// any redirect), but only adopt the final URL as the feed's identity when the
	// redirect chain was entirely permanent — see canonicalURL.
	base := orURL(resp.FinalURL, rawURL)
	if pf, perr := s.parser.Parse(resp.Body, resp.ContentType, base); perr == nil && pf != nil {
		return normalizeFeedURL(canonicalURL(rawURL, resp)), pf, resp, nil
	}
	urls, derr := s.parser.Discover(resp.Body, base)
	if derr != nil || len(urls) == 0 {
		return "", nil, nil, fmt.Errorf("%w: no feed found at URL", ErrValidation)
	}
	resp2, err := s.fetcher.Fetch(ctx, FetchRequest{URL: urls[0]})
	if err != nil || resp2.Status != 200 {
		return "", nil, nil, fmt.Errorf("%w: discovered feed unreachable", ErrValidation)
	}
	base2 := orURL(resp2.FinalURL, urls[0])
	pf, perr := s.parser.Parse(resp2.Body, resp2.ContentType, base2)
	if perr != nil {
		return "", nil, nil, fmt.Errorf("%w: discovered feed unparseable", ErrValidation)
	}
	return normalizeFeedURL(canonicalURL(urls[0], resp2)), pf, resp2, nil
}

// orURL returns final when non-empty, else fallback. Used to pick the base for
// resolving relative links: always prefer the post-redirect URL the bytes came
// from, regardless of whether the redirect was permanent.
func orURL(final, fallback string) string {
	if final != "" {
		return final
	}
	return fallback
}

// canonicalURL upgrades a feed's identity to the post-redirect URL only when the
// redirect chain was entirely permanent (301/308). A temporary (302/307) redirect
// must not rewrite the stored feed_url.
func canonicalURL(current string, resp *FetchResponse) string {
	if resp.PermanentRedirect && resp.FinalURL != "" {
		return resp.FinalURL
	}
	return current
}

// normalizeFeedURL canonicalises a feed URL for dedupe: it lowercases the scheme
// and host, strips the scheme's default port, and drops any fragment. It is
// deliberately conservative — it does NOT touch the path/query, merge http↔https,
// or strip tracker params — because false-merging two genuinely distinct feeds
// silently drops content, whereas a missed dedup only duplicates entries (audit
// B8). Idempotent; returns the trimmed input unchanged if it doesn't parse (URL
// validity is checked separately by the callers).
func normalizeFeedURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname() // drop the default port
	}
	u.Fragment = ""
	return u.String()
}

func (s *FeedService) Refresh(ctx context.Context, userID, feedID ID) error {
	f, err := s.store.GetFeed(ctx, userID, feedID)
	if err != nil {
		return err
	}
	return s.PollFeed(ctx, f)
}

// PollFeed implements FeedPoller: fetch (conditional) → parse → sanitise → upsert → reschedule.
// Fetch/parse errors are recorded on the feed and swallowed (background workers continue).
func (s *FeedService) PollFeed(ctx context.Context, f *Feed) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = s.recordPanic(ctx, f, r)
		}
	}()
	now := s.clk.Now()
	resp, err := s.fetcher.Fetch(ctx, FetchRequest{URL: f.FeedURL, ETag: f.ETag, LastModified: f.LastModified})
	if err != nil {
		return s.recordError(ctx, f, now, err.Error(), 0)
	}
	if resp.NotModified {
		s.adoptPermanentRedirect(ctx, f, resp)
		return s.recordSuccess(ctx, f, now, resp, nil)
	}
	if resp.Status == 429 || resp.Status >= 500 {
		return s.recordError(ctx, f, now, fmt.Sprintf("http %d", resp.Status), resp.RetryAfter)
	}
	if resp.Status != 200 {
		return s.recordError(ctx, f, now, fmt.Sprintf("http %d", resp.Status), 0)
	}
	s.adoptPermanentRedirect(ctx, f, resp)
	// Resolve relative entry links / SiteURL against the post-redirect URL.
	pf, err := s.parser.Parse(resp.Body, resp.ContentType, orURL(resp.FinalURL, f.FeedURL))
	if err != nil {
		// The stored URL served HTML, not a feed — typically a site-page subscribe
		// whose initial discovery died (network blip, the 60s subscribe-budget
		// timeout, or a restart mid-goroutine), leaving the feed pointing at the page.
		// Plain PollFeed would fail parse here forever with no re-discovery. Route it
		// back through the full resolve+discover path so a scheduled poll or manual
		// refresh self-heals and persists the discovered feed URL (audit B10).
		//
		// Gate on ErrorCount > 0 so a HEALTHY feed's single transient 200-HTML blip (a
		// CDN/WAF interstitial, a maintenance page) just records one error and backs
		// off — it must not let a one-off interstitial trigger rediscovery and
		// silently repoint the subscription. A genuinely-stuck feed already carries
		// errors (its initial resolve failed → recordError), so it still self-heals.
		if isHTML(resp.ContentType) && f.ErrorCount > 0 {
			return s.ResolveAndIngest(ctx, f)
		}
		return s.recordError(ctx, f, now, "parse: "+err.Error(), 0)
	}
	return s.recordSuccess(ctx, f, now, resp, pf)
}

// adoptPermanentRedirect migrates a feed's stored URL when a poll followed an
// entirely-permanent (301/308) redirect chain to a new canonical URL, so the feed
// stops paying the redirect hop on every poll and a later re-subscribe to the new
// URL is correctly deduplicated. Unlike the subscribe path, a poll must never fail
// on conflict: if the canonical URL is already subscribed under another row (or any
// other store error occurs), keep the current URL and log — the poll still succeeds
// against the fetched body. Clears the stale conditional-GET validators on adoption
// (they belonged to the old URL); recordSuccess repopulates them from this response.
func (s *FeedService) adoptPermanentRedirect(ctx context.Context, f *Feed, resp *FetchResponse) {
	if !resp.PermanentRedirect || resp.FinalURL == "" || resp.FinalURL == f.FeedURL {
		return
	}
	if err := s.store.SetFeedURL(ctx, f.UserID, f.ID, resp.FinalURL, s.clk.Now()); err != nil {
		if errors.Is(err, ErrConflict) {
			s.log.Warn("feed permanently redirected to an already-subscribed URL; keeping current URL",
				"feed_id", int64(f.ID), "from", f.FeedURL, "to", resp.FinalURL)
		} else {
			s.log.Warn("persisting permanent-redirect URL failed; keeping current URL",
				"feed_id", int64(f.ID), "from", f.FeedURL, "to", resp.FinalURL, "error", err)
		}
		return
	}
	s.log.Info("feed permanently moved; adopted canonical URL",
		"feed_id", int64(f.ID), "from", f.FeedURL, "to", resp.FinalURL)
	f.FeedURL = resp.FinalURL
	f.ETag = ""
	f.LastModified = ""
}

// ingest sanitises and upserts a parsed feed's entries. baseURL is the URL the
// feed bytes were actually read from (post-redirect); relative href/img in entry
// content resolve against it — never the pre-redirect feed URL, which for a
// temporary redirect (not adopted as identity) would bake in broken links.
func (s *FeedService) ingest(ctx context.Context, f *Feed, pf *ParsedFeed, baseURL string) error {
	if pf == nil {
		return nil
	}
	// extract_state is NOT set here: the store derives it from the feed's live
	// fetch_full_content inside the upsert transaction, so entries this poll inserts
	// reflect a concurrent full-content toggle rather than the poll-time snapshot
	// (audit B7).
	now := s.clk.Now()
	// Sanitisation (html.Parse + render + bluemonday, twice per entry) is the poll's
	// dominant steady-state CPU cost. Pre-check which GUIDs already exist unchanged or
	// are tombstoned so we sanitise only genuinely new or hash-changed entries — the
	// hash is derived from RAW parsed text (parse.EntryHash), identical to the stored
	// hash, so deferral is semantically transparent. UpsertEntries re-checks both
	// inside its transaction, so a race between this pre-check and the write is safe.
	guids := make([]string, len(pf.Entries))
	for i, pe := range pf.Entries {
		guids[i] = pe.GUID
	}
	existing, tombstoned, err := s.store.EntryDedup(ctx, f.ID, guids)
	if err != nil {
		return err
	}
	entries := make([]*Entry, 0, len(pf.Entries))
	for _, pe := range pf.Entries {
		if tombstoned[pe.GUID] {
			continue // never resurrect a deleted entry; skip before sanitising
		}
		if h, ok := existing[pe.GUID]; ok && h == pe.Hash {
			continue // unchanged: UpsertEntries would no-op it anyway
		}
		// Undated items (parser leaves PublishedAt zero) would persist as year-1
		// and sink to the permanent bottom of every published-desc list. Fall back
		// to first-seen (ingest) time so ordering matches other feed readers.
		published := pe.PublishedAt
		if published.IsZero() {
			published = now
		}
		entries = append(entries, &Entry{
			UserID: f.UserID, FeedID: f.ID, GUID: pe.GUID, URL: pe.URL, Title: pe.Title,
			Author: pe.Author, Content: s.san.Sanitize(pe.Content, baseURL),
			Summary: s.san.Sanitize(pe.Summary, baseURL), PublishedAt: published,
			Status: StatusUnread, CreatedAt: now,
			Hash: pe.Hash,
		})
	}
	_, err = s.store.UpsertEntries(ctx, f.ID, entries)
	return err
}

// nextCheck computes the adaptive next-poll time for a successfully-polled feed.
// A feed younger than one week has too few observed samples to trust the count
// (its first poll carries historical dates outside the window), so it polls at
// MinInterval until it settles; conditional GET keeps that cheap.
func (s *FeedService) nextCheck(ctx context.Context, f *Feed, now time.Time) time.Time {
	if now.Sub(f.CreatedAt) < Week {
		return s.minCheck(now) // cold start: observe before adapting
	}
	wc, err := s.store.WeeklyEntryCount(ctx, f.ID, now)
	if err != nil {
		// A transient count failure must not silently park an active feed at
		// MaxInterval (the weeklyCount==0 result); re-observe at MinInterval.
		s.log.Warn("weekly entry count", "feed_id", int64(f.ID), "error", err)
		return s.minCheck(now)
	}
	return now.Add(AdaptiveInterval(wc, s.cfg.Schedule, f.TTL, s.cfg.Jitter))
}

// minCheck schedules the next poll one MinInterval out (plus jitter).
func (s *FeedService) minCheck(now time.Time) time.Time {
	d := s.cfg.Schedule.MinInterval
	if s.cfg.Jitter != nil {
		d += s.cfg.Jitter(d)
	}
	return now.Add(d)
}

func (s *FeedService) recordSuccess(ctx context.Context, f *Feed, now time.Time, resp *FetchResponse, pf *ParsedFeed) error {
	if pf != nil {
		if err := s.ingest(ctx, f, pf, orURL(resp.FinalURL, f.FeedURL)); err != nil {
			// A store-layer ingest failure (disk full, I/O error, lock timeout)
			// must not skip rescheduling: returning early here leaves next_check_at
			// in the past, so the feed is re-dispatched (full-body, no conditional
			// GET) every tick. Route through recordError so it backs off instead.
			return s.recordError(ctx, f, now, "ingest: "+err.Error(), 0)
		}
		f.Title = orKeep(pf.Title, f.Title)
		f.SiteURL = orKeep(pf.SiteURL, f.SiteURL)
		f.Description = orKeep(pf.Description, f.Description)
		f.TTL = pf.TTL // poll-owned: refresh from the parsed feed
	}
	// Backfill on every successful poll — including 304 (pf == nil) — so a feed
	// can never persist a blank Title. Idempotent once a title is set.
	f.Title = feedTitle(f.Title, f.FeedURL)
	if resp.ETag != "" {
		f.ETag = resp.ETag
	}
	if resp.LastModified != "" {
		f.LastModified = resp.LastModified
	}
	f.ErrorCount = 0
	f.LastError = ""
	f.CheckedAt = &now
	f.UpdatedAt = now
	f.NextCheckAt = s.nextCheck(ctx, f, now)
	return s.store.UpdateFeed(ctx, f)
}

func (s *FeedService) recordError(ctx context.Context, f *Feed, now time.Time, msg string, retryAfter time.Duration) error {
	f.ErrorCount++
	f.LastError = msg
	f.CheckedAt = &now
	f.UpdatedAt = now
	f.NextCheckAt = PollReschedule(now, s.cfg.Reschedule, f.ErrorCount, retryAfter, s.cfg.Jitter)
	s.log.Warn("feed poll error", "feed_id", int64(f.ID), "url", f.FeedURL, "error", msg)
	// When the poll failed *because* ctx was cancelled/expired (the 60s subscribe
	// budget, or shutdown), reusing it for the write would fail too and the error row
	// would never be recorded — the pending-row-becomes-error-row UX silently never
	// happens (audit B10). Detach only in that case, so a normal poll error still
	// persists on the live request ctx (keeping its deadline and any values); we
	// don't want to unconditionally sever cancellation on every error path.
	pctx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		pctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	return s.store.UpdateFeed(pctx, f)
}

// recordPanic records a recovered pipeline panic as a feed error (with backoff
// reschedule) so a single pathological feed degrades to an error row instead of
// crashing the poll goroutine and staying due forever. See RecoverGuard.
func (s *FeedService) recordPanic(ctx context.Context, f *Feed, r any) error {
	s.log.Error("recovered panic polling feed",
		"feed_id", int64(f.ID), "url", f.FeedURL, "panic", r, "stack", string(debug.Stack()))
	return s.recordError(ctx, f, s.clk.Now(), fmt.Sprintf("panic: %v", r), 0)
}

// EditFeedInput carries the four user-editable feed fields. An empty URL leaves
// the URL unchanged; an empty Title clears the user-rename override.
type EditFeedInput struct {
	Title       string
	URL         string
	CategoryID  *ID
	FullContent bool
}

// EditFeedResult reports which grouping/identity fields changed so the web
// layer can decide between a row swap and a full refresh.
type EditFeedResult struct {
	URLChanged      bool
	CategoryChanged bool
}

// EditFeed applies the four user-editable fields to an owned feed. Title writes
// the user_title override; URL (when changed and well-formed) updates feed_url;
// category and full-content reuse their dedicated setters (full-content only on
// an actual change, to avoid needless extract re-queueing). Returns which
// grouping/identity-affecting fields changed so the caller can decide between a
// row swap and a full refresh.
func (s *FeedService) EditFeed(ctx context.Context, userID, feedID ID, in EditFeedInput) (EditFeedResult, error) {
	var res EditFeedResult
	f, err := s.store.GetFeed(ctx, userID, feedID)
	if err != nil {
		return res, err
	}
	newURL := strings.TrimSpace(in.URL)
	if newURL != "" {
		newURL = normalizeFeedURL(newURL)
		if u, perr := url.Parse(newURL); perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return res, fmt.Errorf("%w: invalid feed URL", ErrValidation)
		}
	}
	if err := s.ensureCategoryOwned(ctx, userID, in.CategoryID); err != nil {
		return res, err
	}
	// Apply the URL change FIRST. It is the only step with a realistic failure — an
	// ErrConflict when the new URL duplicates another subscribed feed — so doing it
	// before the other writes means a conflict leaves nothing else persisted and the
	// "already exists" error the web layer shows is truthful (audit B10). The store
	// writes are not one transaction, but the remaining steps only fail on hard DB
	// errors, not user input.
	if newURL != "" && newURL != f.FeedURL {
		if err := s.store.SetFeedURL(ctx, userID, feedID, newURL, s.clk.Now()); err != nil {
			return res, err
		}
		res.URLChanged = true
	}
	if err := s.store.SetFeedUserTitle(ctx, userID, feedID, strings.TrimSpace(in.Title)); err != nil {
		return res, err
	}
	if !sameID(f.CategoryID, in.CategoryID) {
		if err := s.store.SetFeedCategory(ctx, userID, feedID, in.CategoryID); err != nil {
			return res, err
		}
		res.CategoryChanged = true
	}
	if in.FullContent != f.FetchFullContent {
		if err := s.SetFullContent(ctx, userID, feedID, in.FullContent); err != nil {
			return res, err
		}
	}
	return res, nil
}

func sameID(a, b *ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// EntryStats returns per-feed total and unread counts for the user. Use only where
// every feed's counts are needed (the feeds index page); the list header and single
// feed rows use UnreadCount / FeedStats to avoid an O(all-entries) aggregate.
func (s *FeedService) EntryStats(ctx context.Context, userID ID) (map[ID]FeedEntryStats, error) {
	return s.store.EntryStatsByFeed(ctx, userID)
}

// UnreadCount returns the user's total unread entries (list-header total).
func (s *FeedService) UnreadCount(ctx context.Context, userID ID) (int, error) {
	return s.store.UnreadCount(ctx, userID)
}

// FeedStats returns total/unread counts for a single feed.
func (s *FeedService) FeedStats(ctx context.Context, userID, feedID ID) (FeedEntryStats, error) {
	return s.store.FeedEntryStatsByID(ctx, userID, feedID)
}

func orKeep(newv, old string) string {
	if strings.TrimSpace(newv) != "" {
		return newv
	}
	return old
}

// feedTitle guarantees a feed always has a non-empty display name. Some feeds
// ship a blank <title> but still have entries, so we fall back to the feed URL
// — an empty Title leaves the manage-page link with no clickable text. This is
// the best *automatic* name; the user override (feeds.user_title via
// Feed.DisplayTitle) sits on top of it.
func feedTitle(title, feedURL string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return feedURL
}
