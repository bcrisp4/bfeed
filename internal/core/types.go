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

type Feed struct {
	ID           ID
	UserID       ID
	FeedURL      string
	SiteURL      string
	Title        string
	Description  string
	ETag         string
	LastModified string
	Disabled     bool
	CheckedAt    *time.Time
	NextCheckAt  time.Time
	ErrorCount   int
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type EntryStatus string

const (
	StatusUnread EntryStatus = "unread"
	StatusRead   EntryStatus = "read"
)

func (s EntryStatus) Valid() bool { return s == StatusUnread || s == StatusRead }

type Entry struct {
	ID          ID
	UserID      ID
	FeedID      ID
	GUID        string
	URL         string
	Title       string
	Author      string
	Content     string // sanitised HTML
	Summary     string // sanitised HTML
	PublishedAt time.Time
	Status      EntryStatus
	Starred     bool
	ReadAt      *time.Time
	CreatedAt   time.Time
	Hash        string
}

type Tombstone struct {
	FeedID    ID
	GUID      string
	DeletedAt time.Time
}

type Order int

const (
	OrderPublishedDesc Order = iota // default: newest first
)

// Cursor is the keyset pagination position: the (published_at, id) of the last
// row returned. The next page selects rows strictly "after" it.
type Cursor struct {
	PublishedAt time.Time
	ID          ID
}

// EntryFilter expresses list criteria; zero value = all entries for the user.
type EntryFilter struct {
	FeedID  *ID
	Status  *EntryStatus
	Starred *bool
	Limit   int
	Cursor  *Cursor
	Order   Order
}
