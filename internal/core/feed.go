package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

type FeedServiceConfig struct {
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

// SetFullContent toggles per-feed full-content extraction for an owned feed.
// Enabling backfills ALL existing entries to pending; disabling cancels queued ones.
func (s *FeedService) SetFullContent(ctx context.Context, userID, feedID ID, on bool) error {
	if err := s.store.SetFeedFullContent(ctx, userID, feedID, on); err != nil {
		return err // ErrNotFound if not owned — checked before touching entries
	}
	if on {
		return s.store.MarkFeedEntriesPending(ctx, feedID, s.clk.Now())
	}
	return s.store.CancelFeedExtractions(ctx, feedID)
}

func (s *FeedService) SetCategory(ctx context.Context, userID, feedID ID, categoryID *ID) error {
	if err := s.ensureCategoryOwned(ctx, userID, categoryID); err != nil {
		return err
	}
	return s.store.SetFeedCategory(ctx, userID, feedID, categoryID)
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

// Subscribe validates the URL, fetches it (discovering the feed if HTML), parses,
// creates the feed, runs an initial poll to populate entries, and sets NextCheckAt.
func (s *FeedService) Subscribe(ctx context.Context, userID ID, rawURL string, categoryID *ID, fetchFullContent bool) (*Feed, error) {
	rawURL = strings.TrimSpace(rawURL)
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%w: invalid feed URL", ErrValidation)
	}
	if err := s.ensureCategoryOwned(ctx, userID, categoryID); err != nil {
		return nil, err
	}
	feedURL, pf, resp, err := s.resolveFeed(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	f := &Feed{
		UserID: userID, CategoryID: categoryID, FeedURL: feedURL, SiteURL: pf.SiteURL, Title: feedTitle(pf.Title, feedURL),
		Description: pf.Description, ETag: resp.ETag, LastModified: resp.LastModified,
		NextCheckAt: now, CreatedAt: now, UpdatedAt: now, FetchFullContent: fetchFullContent,
	}
	id, err := s.store.CreateFeed(ctx, f)
	if err != nil {
		return nil, err
	}
	f.ID = id
	if err := s.ingest(ctx, f, pf); err != nil {
		_ = s.store.DeleteFeed(ctx, userID, f.ID) // roll back the partial subscribe
		return nil, err
	}
	f.CheckedAt = &now
	f.NextCheckAt = PollReschedule(now, s.cfg.Reschedule, 0, 0, s.cfg.Jitter)
	f.ErrorCount = 0
	if err := s.store.UpdateFeed(ctx, f); err != nil {
		_ = s.store.DeleteFeed(ctx, userID, f.ID) // roll back the partial subscribe
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
	if pf, perr := s.parser.Parse(resp.Body, rawURL); perr == nil && pf != nil && (pf.Title != "" || len(pf.Entries) > 0) {
		return rawURL, pf, resp, nil
	}
	urls, derr := s.parser.Discover(resp.Body, rawURL)
	if derr != nil || len(urls) == 0 {
		return "", nil, nil, fmt.Errorf("%w: no feed found at URL", ErrValidation)
	}
	resp2, err := s.fetcher.Fetch(ctx, FetchRequest{URL: urls[0]})
	if err != nil || resp2.Status != 200 {
		return "", nil, nil, fmt.Errorf("%w: discovered feed unreachable", ErrValidation)
	}
	pf, perr := s.parser.Parse(resp2.Body, urls[0])
	if perr != nil {
		return "", nil, nil, fmt.Errorf("%w: discovered feed unparseable", ErrValidation)
	}
	return urls[0], pf, resp2, nil
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
func (s *FeedService) PollFeed(ctx context.Context, f *Feed) error {
	now := s.clk.Now()
	resp, err := s.fetcher.Fetch(ctx, FetchRequest{URL: f.FeedURL, ETag: f.ETag, LastModified: f.LastModified})
	if err != nil {
		return s.recordError(ctx, f, now, err.Error(), 0)
	}
	if resp.NotModified {
		return s.recordSuccess(ctx, f, now, resp, nil)
	}
	if resp.Status == 429 || resp.Status >= 500 {
		return s.recordError(ctx, f, now, fmt.Sprintf("http %d", resp.Status), resp.RetryAfter)
	}
	if resp.Status != 200 {
		return s.recordError(ctx, f, now, fmt.Sprintf("http %d", resp.Status), 0)
	}
	pf, err := s.parser.Parse(resp.Body, f.FeedURL)
	if err != nil {
		return s.recordError(ctx, f, now, "parse: "+err.Error(), 0)
	}
	return s.recordSuccess(ctx, f, now, resp, pf)
}

func (s *FeedService) ingest(ctx context.Context, f *Feed, pf *ParsedFeed) error {
	if pf == nil {
		return nil
	}
	state := ExtractNone
	if f.FetchFullContent {
		state = ExtractPending
	}
	entries := make([]*Entry, 0, len(pf.Entries))
	for _, pe := range pf.Entries {
		entries = append(entries, &Entry{
			UserID: f.UserID, FeedID: f.ID, GUID: pe.GUID, URL: pe.URL, Title: pe.Title,
			Author: pe.Author, Content: s.san.Sanitize(pe.Content, f.FeedURL),
			Summary: s.san.Sanitize(pe.Summary, f.FeedURL), PublishedAt: pe.PublishedAt,
			Status: StatusUnread, CreatedAt: s.clk.Now(),
			Hash: pe.Hash, ExtractState: state,
		})
	}
	_, err := s.store.UpsertEntries(ctx, f.ID, entries)
	return err
}

func (s *FeedService) recordSuccess(ctx context.Context, f *Feed, now time.Time, resp *FetchResponse, pf *ParsedFeed) error {
	if pf != nil {
		if err := s.ingest(ctx, f, pf); err != nil {
			return err
		}
		f.Title = feedTitle(orKeep(pf.Title, f.Title), f.FeedURL)
		f.SiteURL = orKeep(pf.SiteURL, f.SiteURL)
		f.Description = orKeep(pf.Description, f.Description)
	}
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
	f.NextCheckAt = PollReschedule(now, s.cfg.Reschedule, 0, 0, s.cfg.Jitter)
	return s.store.UpdateFeed(ctx, f)
}

func (s *FeedService) recordError(ctx context.Context, f *Feed, now time.Time, msg string, retryAfter time.Duration) error {
	f.ErrorCount++
	f.LastError = msg
	f.CheckedAt = &now
	f.UpdatedAt = now
	f.NextCheckAt = PollReschedule(now, s.cfg.Reschedule, f.ErrorCount, retryAfter, s.cfg.Jitter)
	s.log.Warn("feed poll error", "feed_id", int64(f.ID), "url", f.FeedURL, "error", msg)
	return s.store.UpdateFeed(ctx, f)
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
// the best *automatic* name; a future user override (see roadmap A7) would sit
// on top of it.
func feedTitle(title, feedURL string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return feedURL
}
