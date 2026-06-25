package core

import "time"

type ID int64

// DefaultUserID is the single implicit user in the MVP (no auth; tailnet boundary).
const DefaultUserID ID = 1

type User struct {
	ID        ID
	Username  string
	CreatedAt time.Time
}

type Category struct {
	ID     ID
	UserID ID
	Title  string
}

type Feed struct {
	ID               ID
	UserID           ID
	CategoryID       *ID // nil = uncategorised
	FeedURL          string
	SiteURL          string
	Title            string
	Description      string
	ETag             string
	LastModified     string
	Disabled         bool
	FetchFullContent bool
	CheckedAt        *time.Time
	NextCheckAt      time.Time
	ErrorCount       int
	LastError        string
	TTL              time.Duration // publisher-declared min poll interval; 0 = none. Poll-owned.
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type EntryStatus string

const (
	StatusUnread EntryStatus = "unread"
	StatusRead   EntryStatus = "read"
)

func (s EntryStatus) Valid() bool { return s == StatusUnread || s == StatusRead }

type ExtractState string

const (
	ExtractNone    ExtractState = "none"
	ExtractPending ExtractState = "pending"
	ExtractDone    ExtractState = "done"
	ExtractFailed  ExtractState = "failed"
)

type Entry struct {
	ID              ID
	UserID          ID
	FeedID          ID
	GUID            string
	URL             string
	Title           string
	Author          string
	Content         string // sanitised HTML
	Summary         string // sanitised HTML
	PublishedAt     time.Time
	Status          EntryStatus
	Starred         bool
	ReadAt          *time.Time
	CreatedAt       time.Time
	Hash            string
	ExtractState    ExtractState
	ExtractAttempts int
}

// FeedEntryStats holds per-feed entry counts for the feed list and headers.
type FeedEntryStats struct {
	Total  int
	Unread int
}

type Tombstone struct {
	FeedID    ID
	GUID      string
	DeletedAt time.Time
}

type Order int

const (
	OrderPublishedDesc Order = iota // default: newest published first
	OrderReadAtDesc                 // history: most-recently-read first
)

// Cursor is the keyset pagination position: the active order-column value
// (unix seconds — published_at or read_at) and id of the last row returned.
// The next page selects rows strictly "after" it.
type Cursor struct {
	Key int64
	ID  ID
}

// EntryFilter expresses list criteria; zero value = all entries for the user.
type EntryFilter struct {
	FeedID        *ID
	Status        *EntryStatus
	Starred       *bool
	CategoryID    *ID  // filter to one category (via a feeds JOIN)
	Uncategorised bool // feeds with no category; distinct from a nil CategoryID (= all)
	Limit         int
	Cursor        *Cursor
	Order         Order
}
