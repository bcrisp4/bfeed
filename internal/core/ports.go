package core

import (
	"context"
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
}

type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error)
}

type ParsedFeed struct {
	Title, SiteURL, Description string
	TTL                         time.Duration // publisher-declared min interval; 0 = none
	Entries                     []ParsedEntry
}

type ParsedEntry struct {
	GUID, URL, Title, Author string
	Content, Summary         string // RAW; sanitise before persistence
	PublishedAt              time.Time
	Hash                     string // sha256(title|content|summary)
}

type FeedParser interface {
	Parse(data []byte, feedURL string) (*ParsedFeed, error)
	Discover(data []byte, pageURL string) ([]string, error)
}

type Sanitizer interface {
	Sanitize(html, baseURL string) string
}

// Extractor pulls main-article HTML from a fetched page (Readability-style).
type Extractor interface {
	Extract(ctx context.Context, pageURL string, page []byte) (html string, err error)
}

type FeedStore interface {
	CreateFeed(ctx context.Context, f *Feed) (ID, error)
	GetFeed(ctx context.Context, userID, feedID ID) (*Feed, error)
	ListFeeds(ctx context.Context, userID ID) ([]*Feed, error)
	ListDueFeeds(ctx context.Context, now time.Time, limit int) ([]*Feed, error)
	UpdateFeed(ctx context.Context, f *Feed) error
	DeleteFeed(ctx context.Context, userID, feedID ID) error
	SetFeedCategory(ctx context.Context, userID, feedID ID, categoryID *ID) error
	SetFeedFullContent(ctx context.Context, userID, feedID ID, on bool) error
	SetFeedUserTitle(ctx context.Context, userID, feedID ID, title string) error
	EntryStatsByFeed(ctx context.Context, userID ID) (map[ID]FeedEntryStats, error)
	// WeeklyEntryCount counts entries for a feed in [now-week, now], using the
	// entry's published_at when present and falling back to ingest time
	// (created_at) when the publisher omitted a date. System/feed-scoped (no
	// user_id): the feed id already implies its owner.
	WeeklyEntryCount(ctx context.Context, feedID ID, now time.Time) (int, error)
}

type EntryStore interface {
	UpsertEntries(ctx context.Context, feedID ID, entries []*Entry) (inserted []*Entry, err error)
	GetEntry(ctx context.Context, userID, entryID ID) (*Entry, error)
	ListEntries(ctx context.Context, userID ID, f EntryFilter) ([]*Entry, *Cursor, error)
	SetStatus(ctx context.Context, userID ID, ids []ID, s EntryStatus) error
	SetStarred(ctx context.Context, userID ID, ids []ID, starred bool) error
	DeleteEntry(ctx context.Context, userID, entryID ID) error
	ListPendingExtractions(ctx context.Context, now time.Time, limit int) ([]*Entry, error)
	SetEntryContent(ctx context.Context, entryID ID, content string) error
	UpdateExtractState(ctx context.Context, entryID ID, state ExtractState, attempts int, nextAt *time.Time) error
	MarkFeedEntriesPending(ctx context.Context, feedID ID, at time.Time) error
	CancelFeedExtractions(ctx context.Context, feedID ID) error
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
