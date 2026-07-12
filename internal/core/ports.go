package core

import (
	"context"
	"io"
	"time"
)

type Clock interface{ Now() time.Time }

type FetchRequest struct{ URL, ETag, LastModified string }

type FetchResponse struct {
	Status       int
	NotModified  bool
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
	RetryAfter   time.Duration
	// FinalURL is the post-redirect URL the response was actually read from; it
	// equals the request URL when no redirect occurred. Consumers resolve relative
	// links against it (never the pre-redirect request URL).
	FinalURL string
	// PermanentRedirect is true iff at least one redirect occurred and EVERY hop
	// was permanent (301/308) — i.e. FinalURL is the feed's new canonical identity.
	PermanentRedirect bool
}

type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error)
}

// FetchStreamResponse is a fetched response whose body is streamed rather than
// buffered — the image proxy copies it straight to the client so it never holds a
// whole image in memory. The consumer MUST Close Body (which also releases the
// fetcher's per-host token). ContentLength is -1 when the upstream did not declare one.
type FetchStreamResponse struct {
	Status        int
	ContentType   string
	ContentLength int64
	Body          io.ReadCloser
}

type ParsedFeed struct {
	Title, SiteURL, Description string
	TTL                         time.Duration // publisher-declared min interval; 0 = none
	Entries                     []ParsedEntry
}

type ParsedEntry struct {
	GUID, URL, Title, Author string
	Content, Summary         string // RAW; sanitise before persistence
	CommentsURL              string // RSS <comments> discussion URL; "" when absent. NOT in Hash.
	PublishedAt              time.Time
	Hash                     string // sha256(title|content|summary)
}

type FeedParser interface {
	// Parse decodes feed bytes. contentType is the HTTP Content-Type (may be
	// empty); it carries the charset for feeds that declare none in-band.
	Parse(data []byte, contentType, feedURL string) (*ParsedFeed, error)
	Discover(data []byte, pageURL string) ([]string, error)
}

type Sanitizer interface {
	Sanitize(html, baseURL string) string
}

// Extractor pulls main-article HTML from a fetched page (Readability-style).
// contentType is the HTTP Content-Type of the fetched page (may be empty or
// lack a charset); the adapter uses it to decode the page to UTF-8.
type Extractor interface {
	Extract(ctx context.Context, pageURL string, page []byte, contentType string) (html string, err error)
}

type FeedStore interface {
	CreateFeed(ctx context.Context, f *Feed) (ID, error)
	GetFeed(ctx context.Context, userID, feedID ID) (*Feed, error)
	ListFeeds(ctx context.Context, userID ID) ([]*Feed, error)
	ListDueFeeds(ctx context.Context, now time.Time, limit int) ([]*Feed, error)
	UpdateFeed(ctx context.Context, f *Feed) error
	DeleteFeed(ctx context.Context, userID, feedID ID) error
	SetFeedCategory(ctx context.Context, userID, feedID ID, categoryID *ID) error
	// SetFeedFullContent toggles per-feed full-content extraction and reconciles the
	// existing backlog atomically (one transaction): enabling backfills all entries
	// to pending (at schedules the first extraction), disabling cancels queued ones.
	// Doing both in one write means a failure can never leave the flag on with an
	// unqueued backlog, or vice versa (audit B10).
	SetFeedFullContent(ctx context.Context, userID, feedID ID, on bool, at time.Time) error
	SetFeedUserTitle(ctx context.Context, userID, feedID ID, title string) error
	// SetFeedURL changes feed_url, clears the old URL's conditional-GET validators,
	// and moves next_check_at to now so the Poller re-fetches the new URL promptly.
	SetFeedURL(ctx context.Context, userID, feedID ID, url string, now time.Time) error
	EntryStatsByFeed(ctx context.Context, userID ID) (map[ID]FeedEntryStats, error)
	// UnreadCount is the user's total unread entries (index-covered COUNT); FeedEntryStatsByID
	// is total/unread for one feed. Both avoid the O(all-entries) EntryStatsByFeed GROUP BY
	// on the hot list-header / feed-row-poll paths.
	UnreadCount(ctx context.Context, userID ID) (int, error)
	FeedEntryStatsByID(ctx context.Context, userID, feedID ID) (FeedEntryStats, error)
	// WeeklyEntryCount counts entries for a feed in [now-week, now], using the
	// entry's published_at when present and falling back to ingest time
	// (created_at) when the publisher omitted a date. System/feed-scoped (no
	// user_id): the feed id already implies its owner.
	WeeklyEntryCount(ctx context.Context, feedID ID, now time.Time) (int, error)
}

type EntryStore interface {
	UpsertEntries(ctx context.Context, feedID ID, entries []*Entry) (inserted []*Entry, err error)
	// EntryDedup reports, for a batch of GUIDs within a feed, which already exist
	// (guid→content hash) and which are tombstoned, so ingest can sanitise only
	// genuinely new or hash-changed entries. UpsertEntries still re-checks both
	// inside its own transaction, so this is a pure pre-filter optimisation.
	EntryDedup(ctx context.Context, feedID ID, guids []string) (existing map[string]string, tombstoned map[string]bool, err error)
	GetEntry(ctx context.Context, userID, entryID ID) (*Entry, error)
	// ListEntries returns entries with Content/Summary truncated to a list-preview
	// length (list rows render only a short blurb); the full body is available only
	// via GetEntry (reader view).
	ListEntries(ctx context.Context, userID ID, f EntryFilter) ([]*Entry, *Cursor, error)
	SetStatus(ctx context.Context, userID ID, ids []ID, s EntryStatus) error
	SetStarred(ctx context.Context, userID ID, ids []ID, starred bool) error
	DeleteEntry(ctx context.Context, userID, entryID ID) error
	ListPendingExtractions(ctx context.Context, now time.Time, limit int) ([]*Entry, error)
	SetEntryContent(ctx context.Context, entryID ID, content string) error
	UpdateExtractState(ctx context.Context, entryID ID, state ExtractState, attempts int, nextAt *time.Time, reason string) error
	MarkReadByFilter(ctx context.Context, userID ID, f EntryFilter) (int, error)
}

type CategoryStore interface {
	CreateCategory(ctx context.Context, c *Category) (ID, error)
	GetCategory(ctx context.Context, userID, id ID) (*Category, error)
	ListCategories(ctx context.Context, userID ID) ([]*Category, error)
	UpdateCategory(ctx context.Context, c *Category) error
	DeleteCategory(ctx context.Context, userID, id ID) error
	UnreadCountsByCategory(ctx context.Context, userID ID) (map[ID]int, int, error)
}

type SearchIndex interface {
	// Search returns entries with Content/Summary truncated to a list-preview length
	// (results render as a list); the full body is available only via GetEntry.
	Search(ctx context.Context, userID ID, query string, f EntryFilter) ([]*Entry, *Cursor, error)
}

type SettingStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string) error
}

type Store interface {
	FeedStore
	EntryStore
	CategoryStore
	SearchIndex
	SettingStore
}

// FeedPoller polls a single feed (fetch→parse→sanitise→upsert→reschedule).
// Implemented by FeedService; consumed by Poller so polling logic lives in one place.
type FeedPoller interface {
	PollFeed(ctx context.Context, f *Feed) error
}

// EntryScraper scrapes a single entry (fetch→extract→sanitise→persist).
// Implemented by ScrapeService; consumed by Scraper.
type EntryScraper interface {
	ScrapeEntry(ctx context.Context, e *Entry) error
}
