# bfeed — Architecture & Design

> Status: living design document. Guides implementation across sessions.
> Scope: interfaces, internal components, and invariants for **bfeed** ("Ben's feed"),
> a self-hosted RSS/Atom feed reader.
> Source requirements: `~/Obsidian/notes/Idea - Feed reader.md` (do not edit).

---

## 1. Purpose & scope

bfeed is a free, open-source, self-hosted feed reader web app inspired by Miniflux.
It targets **1 to a handful of users (O(1))** running on **Raspberry Pi–class hardware**.
It is a single, self-contained Go binary backed by a single SQLite database file, with
no external infrastructure dependencies.

This document defines the component boundaries, the interfaces between them, the data
model, the background-processing model, and the invariants every implementation session
must preserve. It is the contract; feature work hangs off it.

## 2. Goals & non-goals

**Goals**
- Minimal, content-first UI; serif content; light/dark/system theme; single column; mobile-first.
- Fast on poor connections; small pages; htmx-only interactivity (no heavy JS framework).
- Pure-Go build (no cgo, no shared libraries); simple, idiomatic, testable Go.
- Strong privacy: strip ads/trackers; never persist unsafe HTML.
- Full REST API with per-user bearer tokens; OPML import/export.
- Polite, concurrent background feed updates (≥100 concurrent); full-text search.
- 12-factor configuration; structured logging (slog) and Prometheus metrics throughout.

**Non-goals**
- Multi-tenant scale (we optimise for O(1) users, O(1K) feeds, O(100K) entries).
- Federation, social features, recommendation engines.
- Mobile native apps (PWA / add-to-home-screen only).
- Real-time push (no websockets/SSE); htmx request/response is sufficient.

## 3. Scale & performance targets

| Dimension | Target |
|---|---|
| Users | 1 – handful (O(1)) |
| Feed subscriptions | ≥ 1,000 (O(1K)) |
| Entries/posts | ≥ 100,000 (O(100K)) |
| Concurrent feed fetches | ≥ 100 |
| Hardware | Raspberry Pi class (ARM, modest RAM) |
| Container image | Tiny distroless, pushed to GitHub (GHCR) |

Performance levers: effective SQLite indexes, conditional GET to avoid transfers, WAL mode,
bounded worker pools, and FTS5 for search.

## 4. Technology choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Go | Required; single static binary, great concurrency. |
| Storage | SQLite via **`modernc.org/sqlite`** | Pure-Go driver — no cgo, no shared libs. WAL mode. |
| DB access | **sqlc** (SQL → typed Go over `database/sql`) | No ORM runtime; idiomatic; supports FTS5/pragmas; testable against real SQLite. |
| Migrations | **goose** (library mode, embedded SQL) | Pure Go, boring, embedded via `embed.FS`. |
| Feed parsing | **`mmcdole/gofeed`** | Mature; RSS, Atom, and JSON Feed in one API. |
| Content extraction | **`go-shiori/go-readability`** | Pure-Go Readability port for full-content scrape. |
| HTML sanitisation | **`microcosm-cc/bluemonday`** | Mature allowlist sanitiser; strips active content. |
| HTTP server/router | **stdlib `net/http`** (Go 1.22 `ServeMux`) | Method+pattern routing in stdlib; minimal deps; `httptest`-friendly. |
| Templating | stdlib **`html/template`** + **htmx** | Server-rendered HTML fragments; auto-escaping; tiny client. |
| Password hashing | **argon2id** (`golang.org/x/crypto/argon2`) | OWASP first choice; memory-hard; pure Go; no bcrypt 72-byte footgun. |
| Concurrency limiting | `golang.org/x/sync` (`semaphore`, `errgroup`) | Per-host and global limits; structured fan-out. |
| Logging | stdlib **`log/slog`** | Required; structured, leveled. |
| Metrics | **`prometheus/client_golang`** | Required; `/metrics` endpoint. |

> All non-stdlib libraries are mature and pure-Go. Verify `modernc.org/sqlite` enables FTS5
> at the chosen version during the first implementation session; FTS5 is the search backbone.

## 5. Architectural overview — ports & adapters

bfeed follows a **ports-and-adapters (hexagonal)** layout with a **central domain core**.

- **The core** (`internal/core`) holds domain types, the **services** that implement all
  business logic, and the **interfaces the services consume** (`Store`, `Fetcher`,
  `FeedParser`, `Extractor`, `Sanitizer`, `Clock`). Interfaces are **owned by the consumer**
  (the core), not by the implementer.
- **Driven adapters** implement core interfaces: `store/sqlite`, `fetch`, `parse`, `extract`,
  `sanitize`.
- **Driving adapters** call core services: `web` (HTML + htmx) and `api` (JSON + bearer).
  The **poller** is a driving adapter too — it drives feed-update services on a schedule.
- **Composition root** (`cmd/bfeed`) constructs concrete adapters and injects them into
  services (dependency injection by constructor). Nothing else wires dependencies.

**Dependency rule:** dependencies point **inward**. `core` imports nothing from adapters.
Adapters import `core` (to satisfy interfaces and exchange domain types). `cmd/bfeed`
imports everything to wire it.

```
            ┌────────────────────────── driving adapters ──────────────────────────┐
            │   web (html/htmx)        api (json/bearer)        poller (scheduler)   │
            └───────────────┬───────────────┬───────────────────────┬──────────────┘
                            │  call services │                       │
                    ┌───────▼───────────────▼───────────────────────▼───────┐
                    │                    internal/core                       │
                    │  domain types · services · consumer-owned interfaces   │
                    │  FeedService EntryService SearchService UserService    │
                    │  Poller  Cleaner                                       │
                    └───────┬───────────────┬───────────────┬──────┬────────┘
                            │ Store          │ Fetcher       │ ...  │ Clock
            ┌───────────────▼──┐  ┌──────────▼───┐  ┌────────▼──┐ ┌─▼───────┐
            │  store/sqlite    │  │   fetch      │  │  parse    │ │ extract │  (driven
            │  (sqlc, FTS5,    │  │ (polite GET) │  │ (gofeed)  │ │ sanitize│   adapters)
            │   migrations)    │  └──────────────┘  └───────────┘ └─────────┘
            └──────────────────┘
```

## 6. Package layout

```
cmd/
  bfeed/              composition root: config load, wiring, http server, lifecycle
internal/
  core/               domain types, services, consumer-owned interfaces (no adapter imports)
    types.go            User, Feed, Entry, Category, APIToken, Session, Tombstone, ...
    ports.go            Store (+ sub-interfaces), Fetcher, FeedParser, Extractor, Sanitizer, Clock
    errors.go           sentinel errors (ErrNotFound, ErrConflict, ErrUnauthorized, ...)
    feed.go             FeedService
    entry.go            EntryService
    search.go           SearchService
    user.go             UserService (users, auth, tokens, sessions)
    poller.go           Poller (scheduler + bounded worker pool + per-host limiter)
    cleaner.go          Cleaner (TTL retention + tombstone pruning)
  store/sqlite/       Store implementation: sqlc output, queries/, migrations/ (embed.FS), pragmas
  fetch/              Fetcher: polite conditional HTTP client, per-host limiter, backoff
  parse/              FeedParser: gofeed adapter → ParsedFeed
  extract/            Extractor: go-readability adapter
  sanitize/           Sanitizer: bluemonday policy + tracking mitigation
  web/                HTML handlers, templates (embed.FS), sessions, CSRF, PWA assets, themes
  api/                REST handlers, bearer auth, token scoping, admin guard
  obs/                slog setup, Prometheus registry + metric definitions, DB-instrumentation
  clock/              real Clock (time.Now); fake clock lives in tests
```

Real-time-clock adapter is trivial enough to live in `internal/clock`; service tests inject a fake.

## 7. Domain model (types)

All in `internal/core`. `ID` is `int64` (SQLite rowid). Times are `time.Time` (UTC).

```go
type ID int64

type User struct {
    ID           ID
    Username     string
    PasswordHash string    // PHC-encoded argon2id ($argon2id$v=19$m=...,t=...,p=...$salt$hash)
    IsAdmin      bool
    CreatedAt    time.Time
}

type Category struct {
    ID     ID
    UserID ID
    Title  string
}

type Feed struct {
    ID               ID
    UserID           ID
    CategoryID       *ID           // nil = uncategorised
    FeedURL          string        // canonical feed URL (unique per user)
    SiteURL          string        // human site URL
    Title            string
    Description      string
    ETag             string        // last seen ETag (conditional GET)
    LastModified     string        // last seen Last-Modified (conditional GET)
    FetchInterval    time.Duration // base poll interval (per-feed override of default)
    FetchFullContent bool          // per-feed opt-in: scrape full article at fetch time
    Disabled         bool          // user-disabled or auto-disabled after persistent errors
    LastFetchedAt    *time.Time
    NextFetchAt      time.Time     // scheduler key; advanced by interval or backoff
    ErrorCount       int           // consecutive errors; drives backoff; resets on success
    LastError        string
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

type EntryStatus string
const (
    StatusUnread EntryStatus = "unread"
    StatusRead   EntryStatus = "read"
)

type Entry struct {
    ID          ID
    UserID      ID
    FeedID      ID
    GUID        string      // feed-provided stable id; fallback to hash(url|title)
    URL         string
    Title       string
    Author      string
    Content     string      // SANITISED HTML (never raw)
    Summary     string      // SANITISED HTML
    PublishedAt time.Time
    Status      EntryStatus
    Starred     bool
    ReadAt      *time.Time   // set when marked read; ordering key for history
    CreatedAt   time.Time
    Hash        string       // content hash; detects in-place edits, drives upsert
}

type Tombstone struct {        // prevents resurrection of deleted/expired entries
    FeedID    ID
    GUID      string
    DeletedAt time.Time
}

type APIToken struct {
    ID         ID
    UserID     ID
    Name       string
    Prefix     string      // first ~8 chars, shown for identification
    Hash       string      // SHA-256 of the full token (token itself never stored)
    ReadOnly   bool        // scope: read-only vs full access as the owning user
    LastUsedAt *time.Time
    ExpiresAt  *time.Time  // nil = no expiry
    CreatedAt  time.Time
}

type Session struct {
    ID        string       // random 256-bit id; stored in HttpOnly cookie
    UserID    ID
    ExpiresAt time.Time
    CreatedAt time.Time
}
```

## 8. Core interfaces (consumer-owned ports)

Declared in `internal/core`, implemented by adapters. Every method takes `context.Context`.

```go
// Clock abstracts "now" so time-dependent logic (poll scheduling, backoff deadlines,
// TTL cutoffs, token/session expiry) is deterministically testable.
type Clock interface {
    Now() time.Time
}

// Fetcher performs polite, conditional HTTP GETs with per-host concurrency limiting.
type Fetcher interface {
    Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error)
}
type FetchRequest struct {
    URL, ETag, LastModified string
}
type FetchResponse struct {
    Status       int
    NotModified  bool          // true on HTTP 304
    Body         []byte
    ContentType  string
    ETag         string
    LastModified string
    RetryAfter   time.Duration // parsed from Retry-After when present
}

// FeedParser turns raw feed bytes (RSS/Atom/JSON Feed) into a normalised feed + entries.
type FeedParser interface {
    Parse(data []byte, feedURL string) (*ParsedFeed, error)
}
type ParsedFeed struct {
    Title, SiteURL, Description string
    Entries                    []ParsedEntry
}
type ParsedEntry struct {
    GUID, URL, Title, Author string
    Content, Summary         string    // RAW; must be sanitised before persistence
    PublishedAt              time.Time
}

// Extractor pulls main-article HTML from a fetched page (Readability-style).
type Extractor interface {
    Extract(ctx context.Context, pageURL string, page []byte) (html string, err error)
}

// Sanitizer returns safe HTML, stripping scripts, event handlers, and tracking content.
type Sanitizer interface {
    Sanitize(html string) string
}

// Store is the persistence port, composed of focused sub-interfaces so each service
// depends only on what it uses. The sqlite adapter implements the whole Store.
type Store interface {
    FeedStore
    EntryStore
    SearchIndex
    UserStore
    TokenStore
    SessionStore
    CategoryStore
    Maintenance
}

type FeedStore interface {
    CreateFeed(ctx context.Context, f *Feed) (ID, error)          // unique (user_id, feed_url)
    GetFeed(ctx context.Context, userID, feedID ID) (*Feed, error)
    ListFeeds(ctx context.Context, userID ID) ([]*Feed, error)
    ListDueFeeds(ctx context.Context, now time.Time, limit int) ([]*Feed, error)
    UpdateFeed(ctx context.Context, f *Feed) error
    DeleteFeed(ctx context.Context, userID, feedID ID) error      // cascades entries+FTS, writes tombstones
}

type EntryStore interface {
    // UpsertEntries inserts new entries, updates changed ones (by Hash), skips
    // tombstoned (feed_id, guid). Returns count newly inserted. Keeps FTS in sync.
    UpsertEntries(ctx context.Context, feedID ID, entries []*Entry) (inserted int, err error)
    GetEntry(ctx context.Context, userID, entryID ID) (*Entry, error)
    ListEntries(ctx context.Context, userID ID, f EntryFilter) ([]*Entry, *Cursor, error)
    SetStatus(ctx context.Context, userID ID, ids []ID, s EntryStatus) error  // bulk
    SetStarred(ctx context.Context, userID ID, ids []ID, starred bool) error  // bulk
    DeleteEntry(ctx context.Context, userID, entryID ID) error                // writes tombstone
    DeleteOlderThan(ctx context.Context, userID ID, cutoff time.Time) (int, error) // never starred; writes tombstones
}

type SearchIndex interface {
    Search(ctx context.Context, userID ID, query string, f EntryFilter) ([]*Entry, *Cursor, error)
}

type UserStore interface {
    CreateUser(ctx context.Context, u *User) (ID, error)
    GetUserByID(ctx context.Context, id ID) (*User, error)
    GetUserByUsername(ctx context.Context, name string) (*User, error)
    ListUsers(ctx context.Context) ([]*User, error)
    UpdateUser(ctx context.Context, u *User) error
    DeleteUser(ctx context.Context, id ID) error                 // cascades all user data
    CountUsers(ctx context.Context) (int, error)
}

type TokenStore interface {
    CreateToken(ctx context.Context, t *APIToken) (ID, error)
    GetTokenByHash(ctx context.Context, hash string) (*APIToken, error)
    ListTokens(ctx context.Context, userID ID) ([]*APIToken, error)
    TouchToken(ctx context.Context, id ID, at time.Time) error   // update LastUsedAt
    DeleteToken(ctx context.Context, userID, id ID) error
}

type SessionStore interface {
    CreateSession(ctx context.Context, s *Session) error
    GetSession(ctx context.Context, id string) (*Session, error)
    DeleteSession(ctx context.Context, id string) error
    DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)
}

type CategoryStore interface {
    CreateCategory(ctx context.Context, c *Category) (ID, error)
    ListCategories(ctx context.Context, userID ID) ([]*Category, error)
    UpdateCategory(ctx context.Context, c *Category) error
    DeleteCategory(ctx context.Context, userID, id ID) error     // feeds → uncategorised
}

type Maintenance interface {
    PruneTombstones(ctx context.Context, cutoff time.Time) (int, error)
    DatabaseSizeBytes(ctx context.Context) (int64, error)        // page_count * page_size
}

// EntryFilter expresses list/search criteria; zero value = all entries for the user.
type EntryFilter struct {
    FeedID     *ID
    CategoryID *ID
    Status     *EntryStatus
    Starred    *bool
    Query      string        // FTS5 query (search path only)
    Limit      int
    Cursor     *Cursor       // keyset pagination
    Order      Order         // newest-first default; ReadAt-desc for history
}
```

## 9. Core services

Services are plain structs constructed with their dependencies (the narrow interfaces
above) and a `*slog.Logger`. They contain **all business logic and invariant enforcement**.
HTTP/adapters are dumb translation layers.

### 9.1 FeedService
Subscribe (discover + validate feed URL, dedupe per user), categorise, edit (interval,
full-content flag, enable/disable), delete (cascade + tombstones), OPML import/export.
On subscribe it does one immediate fetch to populate title/entries.

### 9.2 EntryService
List/browse (by feed, category, status, starred, history), get single entry, mark
read/unread, star/unstar, bulk actions, delete single entry (→ tombstone). Read state and
star state are per-user. History = read entries ordered by `ReadAt` desc, bounded by TTL.

### 9.3 SearchService
Thin wrapper over `SearchIndex.Search`; translates a user query string into an FTS5 MATCH,
applies the same user-scoping and filters as listing.

### 9.4 UserService (auth)
User CRUD (admin), password set/verify (argon2id), session create/validate/revoke, API
token mint/list/revoke (returns plaintext token once; stores SHA-256), admin-role checks.
First-run bootstrap admin from config.

### 9.5 Poller (background updates)
Owns the scheduler and bounded worker pool. See §12.

### 9.6 Cleaner (retention)
Periodically deletes entries older than the TTL (never starred; writes tombstones) and
prunes stale tombstones. See §14.

Representative constructor (illustrates DI + consumer-owned interfaces):

```go
func NewPoller(
    store interface { FeedStore; EntryStore; Maintenance },
    fetcher Fetcher, parser FeedParser, extractor Extractor, sanitizer Sanitizer,
    clk Clock, log *slog.Logger, cfg PollerConfig,
) *Poller
```

## 10. Adapters

### 10.1 `store/sqlite` — persistence
- Implements the full `Store`. Queries authored as SQL, compiled to typed Go by **sqlc**.
- **Migrations** embedded via `embed.FS`, applied at startup by **goose** (library mode).
- **Pragmas** on every connection: `journal_mode=WAL`, `foreign_keys=ON`,
  `busy_timeout=5000`, `synchronous=NORMAL`, `cache_size`, `temp_store=MEMORY`.
- **Writer serialisation:** SQLite allows one writer. Use a dedicated write connection
  (a pool capped at 1 for writes) plus a read pool, or rely on `busy_timeout` + WAL.
  Start with `busy_timeout`; escalate to split read/write pools only if `SQLITE_BUSY` shows up.
- **FTS5** external-content virtual table mirrors `entries`; kept in sync by triggers
  (insert/update/delete) so the index can never drift from the source rows.
- Maps driver errors to core sentinels (`ErrNotFound`, `ErrConflict`).

### 10.2 `fetch` — polite HTTP client
- Single shared `*http.Client` with sane timeouts, redirect cap, and a `bfeed/<version> (+url)`
  User-Agent.
- **Conditional GET:** sends `If-None-Match` / `If-Modified-Since` from stored validators;
  surfaces 304 as `NotModified` (no body transfer, no reparse).
- **Per-host limiter:** `map[host]*semaphore.Weighted` (lazily created) caps concurrent
  requests per host (default 1). Serialises politeness per origin.
- Parses `Retry-After`; caps response body size; returns typed errors for timeouts/DNS/TLS.

### 10.3 `parse` — feed parsing
- Wraps `gofeed` (auto-detects RSS/Atom/JSON Feed). Normalises into `ParsedFeed`/`ParsedEntry`.
- Resolves relative URLs against the feed URL. Computes a stable `GUID` (feed id, else
  `hash(url|title)`) and a content `Hash`.

### 10.4 `extract` — full-content scrape
- Wraps `go-readability`. Given a fetched page, returns main-article HTML.
- Used only for feeds with `FetchFullContent=true`, during the update pipeline. Output is
  sanitised before storage like any other content.

### 10.5 `sanitize` — safe HTML & tracking mitigation
- bluemonday allowlist policy: permit semantic content tags; **strip** `<script>`, `<style>`,
  `<iframe>`, `<object>`, `<form>`, and all `on*` event-handler attributes.
- Tracking mitigation: strip known tracking query params (`utm_*`, `fbclid`, `gclid`, …)
  from links; drop 1×1 tracking-pixel images.
- **Optional image proxy** (config-gated, default off): rewrite `<img src>` to a bfeed
  endpoint that fetches images server-side so the client never contacts origin servers.

### 10.6 `web` — HTML + htmx
See §18.

### 10.7 `api` — REST + bearer
See §17.

### 10.8 `obs` — observability
slog handler setup (level/format from config), Prometheus registry, metric definitions
(§20), and helpers to instrument DB calls and HTTP handlers.

## 11. Data model & schema

Per-user ownership: `feeds`, `entries`, `categories`, `api_tokens`, `sessions` all carry
`user_id`. Deleting a user cascades all of it.

```sql
-- users
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

-- categories
CREATE TABLE categories (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  UNIQUE (user_id, title)
);

-- feeds
CREATE TABLE feeds (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  feed_url TEXT NOT NULL,
  site_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  fetch_interval_seconds INTEGER NOT NULL,
  fetch_full_content INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0,
  last_fetched_at TEXT,
  next_fetch_at TEXT NOT NULL,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (user_id, feed_url)
);
CREATE INDEX idx_feeds_due ON feeds (next_fetch_at) WHERE disabled = 0;
CREATE INDEX idx_feeds_user ON feeds (user_id);

-- entries
CREATE TABLE entries (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',     -- sanitised HTML
  summary TEXT NOT NULL DEFAULT '',     -- sanitised HTML
  published_at TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'unread',
  starred INTEGER NOT NULL DEFAULT 0,
  read_at TEXT,
  created_at TEXT NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  UNIQUE (feed_id, guid)
);
CREATE INDEX idx_entries_list   ON entries (user_id, status, published_at DESC);
CREATE INDEX idx_entries_feed   ON entries (feed_id, published_at DESC);
CREATE INDEX idx_entries_starred ON entries (user_id, starred, published_at DESC);
CREATE INDEX idx_entries_history ON entries (user_id, read_at DESC) WHERE status = 'read';
CREATE INDEX idx_entries_ttl    ON entries (published_at) WHERE starred = 0;

-- tombstones: deleted/expired (feed_id, guid) must not be re-created on re-poll
CREATE TABLE tombstones (
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  deleted_at TEXT NOT NULL,
  PRIMARY KEY (feed_id, guid)
);
CREATE INDEX idx_tombstones_age ON tombstones (deleted_at);

-- api_tokens
CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL UNIQUE,           -- SHA-256 of token
  read_only INTEGER NOT NULL DEFAULT 0,
  last_used_at TEXT,
  expires_at TEXT,
  created_at TEXT NOT NULL
);

-- sessions
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,                 -- 256-bit random
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

-- FTS5 full-text index over entries (external content)
CREATE VIRTUAL TABLE entries_fts USING fts5(
  title, content,
  content='entries', content_rowid='id',
  tokenize='unicode61'
);
-- triggers keep entries_fts in sync with entries (insert/update/delete)
```

> `published_at`, `created_at`, etc. stored as ISO-8601 UTC text (sortable). User-scoping is
> enforced in every query (`WHERE user_id = ?`); never trust an id without the owning user.

## 12. Feed update pipeline

**Scheduler** (one goroutine): on a ticker (default 30s) calls
`ListDueFeeds(now, batch)` for feeds where `next_fetch_at <= now AND NOT disabled`, and
sends feed jobs to a bounded channel. The scheduler never blocks longer than a tick;
backpressure from the channel naturally limits how much is enqueued.

**Worker pool** (`N` workers, default ≥100 via `BFEED_FEED_WORKERS`): each worker:

1. Acquire the **per-host** semaphore for the feed's host (politeness).
2. `Fetcher.Fetch` with stored ETag/Last-Modified.
   - **304 Not Modified** → no parse; advance `next_fetch_at += interval`; reset errors. Done.
   - **2xx** → continue.
   - **error / 429 / 5xx** → record `last_error`, increment `error_count`, set
     `next_fetch_at` via **exponential backoff with jitter** (honour `Retry-After`), capped
     at a max interval; auto-disable after a high consecutive-error threshold. Done.
3. `FeedParser.Parse` → `ParsedFeed`.
4. For each parsed entry: skip if `(feed_id, guid)` is tombstoned; sanitise content/summary.
5. If `FetchFullContent` and content is thin/empty: `Fetcher.Fetch` the entry URL
   (host-limited), `Extractor.Extract`, then **sanitise** the result.
6. `EntryStore.UpsertEntries` (insert new, update changed-by-hash) — FTS stays in sync.
7. Update feed metadata (title/site/description, new ETag/Last-Modified), reset `error_count`,
   set `next_fetch_at += interval`.
8. Release the per-host semaphore.

Global concurrency = worker count. Per-host concurrency = host semaphore (default 1).
All steps honour `ctx` cancellation for graceful shutdown.

## 13. Content extraction pipeline

Triggered inside the update pipeline (step 5) only for `FetchFullContent` feeds, or when a
parsed entry has no usable content. Reuses the polite `Fetcher` (so host limits/backoff
apply to article fetches too). Extracted HTML is **always sanitised** before persistence —
identical safety path as feed-provided content. Extraction failures are logged and
non-fatal; the entry is stored with whatever the feed provided.

## 14. Retention, cleanup & tombstones

- **Cleaner** runs on an interval (`BFEED_CLEANUP_INTERVAL`, default daily).
- Deletes entries with `published_at < now - TTL` **and** `starred = 0` **and** not the
  feed's still-current items, writing a **tombstone** for each so re-polling can't resurrect
  them. TTL default generous (e.g. 90 days), configurable via `BFEED_ENTRY_TTL_DAYS`.
- **Starred/saved entries are never expired.**
- Deleting a feed cascades its entries + FTS rows and writes tombstones.
- **Tombstone pruning:** tombstones older than a window longer than any feed would re-list
  (`PruneTombstones`, e.g. 2× TTL) are removed to bound growth, on the assumption feeds do
  not re-publish items that old.

## 15. Full-text search

- Backed by **SQLite FTS5** (`entries_fts`), kept in sync with `entries` via triggers.
- `SearchService` maps the user query to an FTS5 `MATCH`, intersects with `user_id` scope and
  any `EntryFilter` (feed/category/status/starred), orders by relevance or recency, paginates
  via keyset cursor.
- Tombstoned/deleted entries are removed from FTS by the delete trigger, so they never appear.

## 16. Authentication & authorization

- **Web sessions:** username + password → argon2id verify → random 256-bit session id stored
  in DB and in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Validated per request; expired
  sessions rejected and reaped by `DeleteExpiredSessions`.
- **CSRF:** synchroniser token in a cookie + form/header; htmx configured to send it on
  mutating requests. GET is safe; all state-changing web routes require a valid token.
- **API:** `Authorization: Bearer <token>`. Middleware SHA-256s the token, looks up by hash,
  checks expiry, resolves the owning user, updates `LastUsedAt`. `ReadOnly` tokens may only
  hit safe methods.
- **Tokens** are per-user, act as that user, shown in plaintext once at creation (only the
  hash is stored). Users manage their own tokens.
- **Admin role:** `is_admin` users access `/admin` (web) and user-management API routes.
  Admin guard middleware enforces it.
- **Bootstrap:** on first start with zero users, create an admin from
  `BFEED_ADMIN_USERNAME` / `BFEED_ADMIN_PASSWORD`. Self-registration is not supported.

## 17. REST API surface (v1)

JSON, versioned under `/v1`, bearer-authenticated. Representative routes:

```
GET    /v1/feeds                 list feeds
POST   /v1/feeds                 subscribe (body: feed_url, category_id?, options)
GET    /v1/feeds/{id}            feed detail
PUT    /v1/feeds/{id}            edit (category, interval, full-content, disabled)
DELETE /v1/feeds/{id}            delete feed (+entries, +tombstones)
GET    /v1/feeds/{id}/entries    entries for a feed (filters, pagination)
POST   /v1/feeds/{id}/refresh    force refresh now

GET    /v1/categories            list / POST create
PUT    /v1/categories/{id}       rename / DELETE delete

GET    /v1/entries               list (status, starred, feed_id, category_id, cursor)
GET    /v1/entries/{id}          entry detail
PUT    /v1/entries/{id}          set status / starred
DELETE /v1/entries/{id}          delete (+tombstone)
POST   /v1/entries/bulk          bulk status/starred over ids or a filter

GET    /v1/search?q=...          full-text search

GET    /v1/export                OPML export
POST   /v1/import                OPML import

GET    /v1/tokens                list own tokens / POST mint / DELETE revoke

GET    /v1/users                 admin: list / POST create
PUT    /v1/users/{id}            admin: edit / DELETE delete

GET    /healthz  /readyz         liveness/readiness (unauthenticated)
GET    /metrics                  Prometheus (bind/guard per config)
```

Errors return a small JSON envelope `{ "error": { "code", "message" } }`; core sentinels map
to status codes (`ErrNotFound`→404, `ErrConflict`→409, `ErrUnauthorized`→401, validation→422).

## 18. Web UI

- Server-rendered `html/template`, progressively enhanced with **htmx**. No SPA framework.
- Single-column, content-first, serif body text. Light/dark/system theme via a CSS
  `prefers-color-scheme` baseline plus a user toggle (persisted in a cookie).
- htmx drives: mark read/unread, star, bulk actions, pagination/infinite scroll, refresh —
  each returns an HTML fragment, swapped in place. Full-page loads stay small and fast.
- Views: unread list (home), all feeds, single feed, categories, starred, history, search,
  settings, login/logout, admin/users.
- **PWA / add-to-home-screen:** `manifest.webmanifest`, app icons, and
  `apple-touch-icon` + meta tags so iOS can install it. Service worker kept minimal/optional
  (offline app-shell only) to honour the "minimal JS" requirement.
- Static assets (CSS, htmx, icons, manifest) embedded via `embed.FS` and served with cache
  headers; no build step, no bundler.

## 19. OPML import/export

- **Export:** `FeedService` emits OPML 2.0 with categories as `<outline>` groups. Plaintext,
  no credentials.
- **Import:** parse OPML, create missing categories, subscribe to each feed (dedupe per user
  by `feed_url`), skip duplicates. Best-effort: malformed entries are reported, not fatal.

## 20. Observability

**slog** everywhere: leveled, structured (JSON in prod, text in dev — config). High-cardinality
detail goes to logs: per-feed update timing, per-feed errors with URL/status, request ids,
user ids. Each background job and HTTP request carries correlating fields.

**Prometheus** metrics (low-cardinality only) answer the required questions:

| Question | Metric |
|---|---|
| How many users? | `bfeed_users_total` (gauge) |
| Subscriptions per user? | `bfeed_feeds_total{user}` (gauge; safe at O(1) users) |
| Entries total? | `bfeed_entries_total` (gauge) |
| Entries per feed? | not a labelled metric (high cardinality) — via admin view / logs |
| Time to update feeds? | `bfeed_feed_update_duration_seconds` (histogram) |
| Queued / in progress? | `bfeed_feed_update_queue_depth`, `bfeed_feed_update_inflight` (gauges) |
| Per-feed update time? | distribution via the histogram above; individual values → logs |
| Feed update errors? | `bfeed_feed_update_errors_total{reason}` (counter) |
| Other errors? | `bfeed_errors_total{component}` (counter) + logs |
| Database size? | `bfeed_db_size_bytes` (gauge; `page_count*page_size`) |
| DB operation latency? | `bfeed_db_query_duration_seconds{op}` (histogram) |

HTTP metrics: `bfeed_http_requests_total{route,method,status}`,
`bfeed_http_request_duration_seconds{route}`.

## 21. Configuration (12-factor)

Environment variables, validated at startup, with sensible defaults:

```
BFEED_LISTEN_ADDR            default :8080
BFEED_BASE_URL               external URL (cookies, OPML, image proxy, UA)
BFEED_DATABASE_PATH          default ./bfeed.db
BFEED_LOG_LEVEL              debug|info|warn|error  (default info)
BFEED_LOG_FORMAT             json|text              (default json)
BFEED_FEED_WORKERS           default 100
BFEED_HOST_CONCURRENCY       default 1
BFEED_DEFAULT_FETCH_INTERVAL default 1h
BFEED_ENTRY_TTL_DAYS         default 90
BFEED_CLEANUP_INTERVAL       default 24h
BFEED_SESSION_TTL            default 720h (30d)
BFEED_IMAGE_PROXY            on|off                 (default off)
BFEED_METRICS_ADDR           optional separate bind for /metrics
BFEED_ADMIN_USERNAME         bootstrap admin (first run only)
BFEED_ADMIN_PASSWORD         bootstrap admin (first run only)
```

## 22. Concurrency & lifecycle

- `cmd/bfeed` builds a root `context.Context` cancelled on SIGINT/SIGTERM.
- Startup: load+validate config → open SQLite (pragmas) → run migrations (goose) →
  bootstrap admin if needed → construct adapters → construct services → start poller
  scheduler, worker pool, and cleaner (goroutines) → start HTTP server.
- Shutdown: cancel context → HTTP `Shutdown` (drain in-flight requests) → scheduler stops
  enqueuing → workers finish/abort current fetch on ctx → close DB. Use `errgroup`/`sync.WaitGroup`
  to wait for clean drain within a timeout.

## 23. Error handling conventions

- Core defines sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrUnauthorized`,
  `ErrValidation`). Adapters wrap driver errors with `%w` and map to sentinels.
- Services return errors up; driving adapters translate to HTTP status / HTML.
- Background workers log and continue — one bad feed never halts the pool.
- No silent failures: every swallowed/decided-non-fatal error is logged with context.

## 24. Testing strategy (TDD: Red/Green/Refactor)

- **Core services:** unit-tested with in-memory fakes implementing the port interfaces and a
  **fake `Clock`** — fast, deterministic, no I/O. This is where invariant tests live.
- **`store/sqlite`:** integration-tested against a real SQLite file/`:memory:` — exercises
  sqlc queries, migrations, FTS sync triggers, cascade deletes, tombstone behaviour.
- **`fetch`:** tested with `httptest.Server` — conditional GET (304), Retry-After, timeouts,
  per-host limiting.
- **`parse`/`extract`/`sanitize`:** golden-file tests over fixture feeds/HTML, incl. malicious
  HTML asserting active content is stripped.
- **`api`/`web`:** `httptest` handler tests — auth, CSRF, scoping, status mapping, fragments.
- **End-to-end smoke:** wire real components, subscribe to a local test feed, assert entries
  appear, search works, mark-read/star/delete behave.

## 25. Deployment

- Single static binary (`CGO_ENABLED=0`), templates/assets/migrations embedded — nothing to
  ship alongside but the SQLite file.
- **Tiny distroless** container (`gcr.io/distroless/static`), multi-arch (amd64 + arm64 for
  Pi), built and pushed to **GHCR** via GitHub Actions.
- Runs as non-root; data dir is a mounted volume holding `bfeed.db` (+ WAL/SHM).
- Stateless except the SQLite file; restart-safe (migrations idempotent, scheduler self-heals).

## 26. Invariants (the contract)

These hold across all sessions. Tests must defend them.

**Safety**
1. HTML is **always** sanitised before persistence. Raw feed/extracted HTML never reaches the DB.
2. Sanitised HTML contains no `<script>`, event handlers, or active embeds.

**Politeness**
3. Feed fetches always attempt conditional GET when validators are stored.
4. Per-host concurrency never exceeds the configured cap; global concurrency never exceeds
   the worker count.
5. On error/429/5xx, the next fetch is delayed by exponential backoff with jitter, honouring
   `Retry-After`; no aggressive immediate retries.

**Data integrity**
6. A tombstoned `(feed_id, guid)` is never re-created by polling.
7. Starred/saved entries are never deleted by TTL cleanup.
8. Deleting a feed removes its entries and FTS rows and leaves tombstones.
9. `entries_fts` always reflects current `entries` (no orphan or stale index rows).
10. `(feed_id, guid)` is unique; re-fetched entries upsert, never duplicate.

**Authorization & isolation**
11. Every data query is scoped by `user_id`; no cross-user read/write is possible via id alone.
12. Every API request resolves to a user via a hashed token; read-only tokens cannot mutate.
13. Admin-only routes require `is_admin`.

**Architecture**
14. `internal/core` imports no adapter package; dependencies point inward.
15. Interfaces are declared by the consuming core; adapters depend on core, not vice-versa.
16. Time-dependent logic uses the injected `Clock`, never `time.Now()` directly.

## 27. Open questions / future

- **License** (requirements: TBD) — pick an OSI license (e.g. AGPL-3.0 or MIT) before release.
- **Image proxy** default — ships off; revisit default once privacy/load trade-off is measured.
- **Service worker scope** — start with installability only; add offline reading later if wanted.
- **Read/write connection split** — adopt only if `busy_timeout` proves insufficient under load.
```
