# bfeed — Design

> Status: living design document. Guides implementation across sessions.
> Scope: interfaces, internal components, and invariants for **bfeed** ("Ben's feed"),
> a self-hosted RSS/Atom feed reader.
> Source requirements: `~/Obsidian/notes/Idea - Feed reader.md` (do not edit).
> Several decisions below (adaptive scheduler, polite polling, SQLite schema, metrics) are
> grounded in research — see **§29 Research basis & sources**.

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
- Strong privacy: strip ads/trackers; never persist unsafe HTML; proxy images by default.
- Full REST API with per-user bearer tokens; OPML import/export.
- Polite, concurrent background feed updates (≥100 concurrent capacity); full-text search.
- Adaptive refresh: poll active feeds more often, quiet feeds less.
- 12-factor configuration; structured logging (slog) and Prometheus metrics throughout.

**Non-goals**
- Multi-tenant scale (we optimise for O(1) users, O(1K) feeds, O(100K) entries).
- Federation, social features, recommendation engines.
- Native mobile apps; **no service worker and no offline mode** — add-to-home-screen only.
- Real-time push (no websockets/SSE); htmx request/response is sufficient.

## 3. Scale & performance targets

| Dimension | Target |
|---|---|
| Users | 1 – handful (O(1)) |
| Feed subscriptions | ≥ 1,000 (O(1K)) |
| Entries/posts | ≥ 100,000 (O(100K)) |
| Concurrent feed fetches | ≥ 100 (capacity; bounded per-host for politeness) |
| Hardware | Raspberry Pi class (ARM, modest RAM, SD-card IO) |
| Container image | Tiny distroless, pushed to GitHub (GHCR) |

Performance levers: effective SQLite indexes, conditional GET to avoid transfers, WAL mode,
INTEGER-epoch time columns, bounded worker pools, and FTS5 for search.

## 4. Technology choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Go | Required; single static binary, great concurrency. |
| Storage | SQLite via **`modernc.org/sqlite`** | Pure-Go driver — no cgo, no shared libs. WAL + STRICT + FTS5. |
| DB access | **sqlc** (SQL → typed Go over `database/sql`) | No ORM runtime; idiomatic; supports FTS5/pragmas; testable against real SQLite. |
| Migrations | **goose** (library mode, embedded SQL) | Pure Go, boring, embedded via `embed.FS`. |
| Feed parsing | **`mmcdole/gofeed`** | Mature; RSS, Atom, and JSON Feed in one API. |
| Content extraction | **`codeberg.org/readeck/go-readability/v2`** | go-shiori/go-readability is **archived (2025-12-30)**; this maintained fork (MIT, pure Go, no cgo) is the replacement. Greenfield → start on **v2** (tracks Readability.js v0.6; best speed/memory). API note: some `Article` values are accessed as methods, not fields. |
| HTML sanitisation | **`microcosm-cc/bluemonday`** | Mature allowlist sanitiser; strips active content. |
| HTTP server/router | **stdlib `net/http`** (Go 1.22 `ServeMux`) | Method+pattern routing in stdlib; minimal deps; `httptest`-friendly. |
| Templating | stdlib **`html/template`** + **htmx** | Server-rendered HTML fragments; auto-escaping; tiny client. |
| Password hashing | **argon2id** (`golang.org/x/crypto/argon2`) | OWASP first choice; memory-hard; pure Go; no bcrypt 72-byte footgun. |
| Concurrency limiting | `golang.org/x/sync` (`semaphore`, `errgroup`) + **`golang.org/x/time/rate`** | Per-host concurrency caps *and* per-host token-bucket rate limits; structured fan-out. |
| Logging | stdlib **`log/slog`** | Required; structured, leveled. |
| Metrics | **`prometheus/client_golang`** | Required; `/metrics`. DB metrics come only from its built-in `collectors.NewDBStatsCollector` (`go_sql_*`). |

> All non-stdlib libraries are mature and pure-Go. Confirm `modernc.org/sqlite` enables FTS5
> and STRICT (SQLite ≥ 3.37) at the chosen version during the first implementation session.

## 5. Architectural overview — ports & adapters

bfeed follows a **ports-and-adapters (hexagonal)** layout with a **central domain core**.

- **The core** (`internal/core`) holds domain types, the **services** that implement all
  business logic, and the **interfaces the services consume** (`Store`, `Fetcher`,
  `FeedParser`, `Extractor`, `Sanitizer`, `Clock`). Interfaces are **owned by the consumer**
  (the core), not by the implementer.
- **Driven adapters** implement core interfaces: `store/sqlite`, `fetch`, `parse`, `extract`,
  `sanitize`.
- **Driving adapters** call core services: `web` (HTML + htmx) and `api` (JSON + bearer).
  The **poller** and **cleaner** are driving adapters too — they drive core services on a schedule.
- **Composition root** (`cmd/bfeed`) constructs concrete adapters and injects them into
  services (dependency injection by constructor). Nothing else wires dependencies.

**Dependency rule:** dependencies point **inward**. `core` imports nothing from adapters.
Adapters import `core` (to satisfy interfaces and exchange domain types). `cmd/bfeed`
imports everything to wire it.

```
            ┌────────────────────────── driving adapters ──────────────────────────┐
            │   web (html/htmx)     api (json/bearer)     poller        cleaner      │
            └───────────────┬───────────────┬───────────────┬──────────────┬────────┘
                            │  call services │               │              │
                    ┌───────▼───────────────▼───────────────▼──────────────▼───────┐
                    │                       internal/core                           │
                    │  domain types · services · consumer-owned interfaces          │
                    │  FeedService EntryService SearchService UserService           │
                    │  Poller(scheduler+pools)  Cleaner(retention)                  │
                    └───────┬───────────────┬───────────────┬──────┬────────────────┘
                            │ Store          │ Fetcher       │ ...  │ Clock
            ┌───────────────▼──┐  ┌──────────▼───┐  ┌────────▼──┐ ┌─▼───────┐
            │  store/sqlite    │  │   fetch      │  │  parse    │ │ extract │  (driven
            │  (sqlc, FTS5,    │  │ (polite GET, │  │ (gofeed)  │ │ sanitize│   adapters)
            │   STRICT, migr.) │  │  host limits)│  └───────────┘ └─────────┘
            └──────────────────┘  └──────────────┘
```

## 6. Package layout

```
cmd/
  bfeed/              composition root + CLI subcommand dispatch (§22): config, wiring, lifecycle
internal/
  core/               domain types, services, consumer-owned interfaces (no adapter imports)
    types.go            User, Feed, Entry, Category, APIToken, Session, Tombstone, ...
    ports.go            Store (+ sub-interfaces), Fetcher, FeedParser, Extractor, Sanitizer, Clock
    errors.go           sentinel errors (ErrNotFound, ErrConflict, ErrUnauthorized, ...)
    feed.go             FeedService
    entry.go            EntryService
    search.go           SearchService
    user.go             UserService (users, auth, tokens, sessions)
    schedule.go         adaptive interval computation (pure function, unit-tested)
    poller.go           Poller: scheduler + feed-poll pool + article-scrape pool + host limiter
    cleaner.go          Cleaner (TTL retention + tombstone pruning)
  store/sqlite/       Store impl: sqlc output, queries/, migrations/ (embed.FS), pragmas, pools
  fetch/              Fetcher: polite conditional HTTP client, per-host semaphore + rate limiter
  parse/              FeedParser: gofeed adapter → ParsedFeed
  extract/            Extractor: readeck/go-readability adapter
  sanitize/           Sanitizer: bluemonday policy + tracking mitigation + image-proxy rewrite
  imgproxy/           image proxy handler: signed URLs, SSRF guard, cache (see §10.6)
  web/                HTML handlers, templates (embed.FS), sessions, CSRF, PWA assets, themes
  api/                REST handlers, bearer auth, token scoping, admin guard
  observability/      slog setup, Prometheus registry + metric definitions
  clock/              real Clock (time.Now); fake clock lives in tests
```

## 7. Domain model (types)

All in `internal/core`. `ID` is `int64` (SQLite rowid). Times are Go `time.Time` (UTC),
persisted as INTEGER Unix seconds (§11).

```go
type ID int64

type User struct {
    ID           ID
    Username     string
    PasswordHash string    // PHC-encoded argon2id ($argon2id$v=19$m=...,t=...,p=...$salt$hash)
    IsAdmin      bool
    EntryTTLDays *int       // per-user retention override; nil = use server default (§14)
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
    FetchFullContent bool          // per-feed opt-in: scrape full article at fetch time
    Disabled         bool          // user-disabled (auto-disable is NOT used; see backoff §12)
    CheckedAt        *time.Time     // last poll attempt time
    NextCheckAt      time.Time      // scheduler key; set by adaptive interval / backoff
    ErrorCount       int            // consecutive errors; drives graceful backoff; resets on success
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
    UserID      ID          // denormalised from feed; powers the hot (user,status,published) index
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

// Fetcher performs polite, conditional HTTP GETs with per-host concurrency AND rate limiting.
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
    RetryAfter   time.Duration // parsed from Retry-After / 429 / 503 when present
}

// FeedParser turns raw feed bytes (RSS/Atom/JSON Feed) into a normalised feed + entries.
type FeedParser interface {
    Parse(data []byte, feedURL string) (*ParsedFeed, error)
}
type ParsedFeed struct {
    Title, SiteURL, Description string
    TTL                        time.Duration // feed-supplied <ttl>/syndication hint, 0 if none
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

// Sanitizer returns safe HTML, stripping scripts, event handlers, trackers, and (when the
// image proxy is enabled) rewriting img sources to signed proxy URLs.
type Sanitizer interface {
    Sanitize(html, baseURL string) string
}

// Store is the persistence port, composed of focused sub-interfaces so each service
// depends only on what it uses. The sqlite adapter implements the whole Store.
type Store interface {
    FeedStore; EntryStore; SearchIndex; UserStore
    TokenStore; SessionStore; CategoryStore; Maintenance
}

type FeedStore interface {
    CreateFeed(ctx context.Context, f *Feed) (ID, error)          // unique (user_id, feed_url)
    GetFeed(ctx context.Context, userID, feedID ID) (*Feed, error)
    ListFeeds(ctx context.Context, userID ID) ([]*Feed, error)
    ListDueFeeds(ctx context.Context, now time.Time, limit int) ([]*Feed, error) // next_check_at<=now, !disabled
    UpdateFeed(ctx context.Context, f *Feed) error                // persists NextCheckAt/CheckedAt/ErrorCount/etag...
    WeeklyEntryCount(ctx context.Context, feedID ID, now time.Time) (int, error) // spacing-based virtual count (§12)
    DeleteFeed(ctx context.Context, userID, feedID ID) error      // cascades entries+FTS, writes tombstones
}

type EntryStore interface {
    // UpsertEntries inserts new entries, updates changed ones (by Hash), skips
    // tombstoned (feed_id, guid). Returns the entries newly inserted (for scrape enqueue).
    UpsertEntries(ctx context.Context, feedID ID, entries []*Entry) (inserted []*Entry, err error)
    SetEntryContent(ctx context.Context, entryID ID, content string) error // post-extraction write
    GetEntry(ctx context.Context, userID, entryID ID) (*Entry, error)
    ListEntries(ctx context.Context, userID ID, f EntryFilter) ([]*Entry, *Cursor, error)
    SetStatus(ctx context.Context, userID ID, ids []ID, s EntryStatus) error  // bulk
    SetStarred(ctx context.Context, userID ID, ids []ID, starred bool) error  // bulk
    DeleteEntry(ctx context.Context, userID, entryID ID) error                // writes tombstone
    // DeleteExpired removes entries that are read AND not starred AND published before cutoff,
    // writing a tombstone for each. Unread and starred entries are never touched. (§14)
    DeleteExpired(ctx context.Context, userID ID, cutoff time.Time) (int, error)
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
    TouchToken(ctx context.Context, id ID, at time.Time) error
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
    DatabaseStats(ctx context.Context) (DBStats, error)          // page_count*page_size, freelist
    Optimize(ctx context.Context) error                          // PRAGMA optimize / wal_checkpoint
}

// EntryFilter expresses list/search criteria; zero value = all entries for the user.
type EntryFilter struct {
    FeedID, CategoryID *ID
    Status             *EntryStatus
    Starred            *bool
    Query              string        // FTS5 query (search path only)
    Limit              int
    Cursor             *Cursor       // keyset pagination (published_at, id)
    Order              Order         // newest-first default; ReadAt-desc for history
}
```

## 9. Core services

Services are plain structs constructed with their dependencies (the narrow interfaces
above) and a `*slog.Logger`. They contain **all business logic and invariant enforcement**.
HTTP/adapters are dumb translation layers.

### 9.1 FeedService
Subscribe (discover + validate feed URL, dedupe per user), categorise, edit (full-content
flag, enable/disable), delete (cascade + tombstones), OPML import/export. On subscribe it
does one immediate fetch to populate title/entries and sets the first `NextCheckAt`.

### 9.2 EntryService
List/browse (by feed, category, status, starred, history), get single entry, mark
read/unread, star/unstar, bulk actions, delete single entry (→ tombstone). Read state and
star state are per-user. History = read entries ordered by `ReadAt` desc, bounded by TTL.

### 9.3 SearchService
Thin wrapper over `SearchIndex.Search`; maps a user query into an FTS5 MATCH, applies the
same user-scoping and filters as listing.

### 9.4 UserService (auth)
User CRUD (admin), password set/verify (argon2id), session create/validate/revoke, API
token mint/list/revoke (returns plaintext token once; stores SHA-256), admin-role checks,
first-run bootstrap admin from config.

### 9.5 Poller
Adaptive scheduler + two bounded worker pools (feed poll, article scrape) + per-host limiter. See §12–13.

### 9.6 Cleaner
Deletes read+unsaved entries older than each user's TTL (writes tombstones), prunes stale
tombstones, and runs SQLite maintenance. See §14.

Representative constructor (illustrates DI + consumer-owned interfaces):

```go
func NewPoller(
    store interface{ FeedStore; EntryStore },
    fetcher Fetcher, parser FeedParser, extractor Extractor, sanitizer Sanitizer,
    clk Clock, log *slog.Logger, cfg PollerConfig,
) *Poller
```

## 10. Adapters

### 10.1 `store/sqlite` — persistence
- Implements the full `Store`. Queries authored as SQL, compiled to typed Go by **sqlc**.
- **Migrations** embedded via `embed.FS`, applied at startup by **goose** (library mode).
- **STRICT tables** everywhere (real type checking; SQLite ≥ 3.37). Only `INTEGER/REAL/TEXT/
  BLOB/ANY` column types; no `VARCHAR`/`DATETIME`/`BOOLEAN`.
- **Connection PRAGMAs** (set on every pooled connection; `journal_mode` is persistent):
  `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON` (off by default — the classic
  footgun), `busy_timeout=5000`, `temp_store=MEMORY`, `cache_size=-8000` (~8 MiB). Leave
  `mmap_size` at default.
- **Single-writer concurrency:** start with one pool at `SetMaxOpenConns(1)` — at O(1) users
  this removes all in-process `SQLITE_BUSY` risk and is the boring default; `busy_timeout` is
  the backstop against the WAL checkpointer. Escalate to split **writer (MaxOpenConns=1) +
  reader (N) pools** on the same file only if read latency under writes is measured to matter.
- **FTS5** external-content virtual table mirrors `entries`, kept in sync by the canonical
  insert/delete/update triggers (delete uses the FTS5 `'delete'` command with *old* values).
- **Maintenance:** `PRAGMA optimize` periodically; `PRAGMA wal_checkpoint(TRUNCATE)` after
  large TTL deletes to reclaim WAL space; FTS5 `'optimize'` occasionally.
- Maps driver errors to core sentinels (`ErrNotFound`, `ErrConflict`).

### 10.2 `fetch` — polite HTTP client
- Single shared `*http.Client` with sane timeouts, redirect cap, capped response body size,
  and a `bfeed/<version> (+<base-url>)` User-Agent.
- **Conditional GET:** sends `If-None-Match` / `If-Modified-Since` from stored validators;
  surfaces 304 as `NotModified` (no body transfer, no reparse) — the single biggest polling win.
- **Per-host politeness, two layers combined per request:**
  1. **global semaphore** (worker-count ceiling) →
  2. **per-host semaphore** (default cap **3**) →
  3. **per-host token-bucket rate limiter** (`x/time/rate`, default **1 req/s, burst 3**) via
     `limiter.Wait(ctx)`.
  A lazily-built `map[host]{*semaphore, *rate.Limiter}` guarded by a mutex, with an
  idle-eviction janitor so the map can't grow unbounded.
- **Backpressure signals:** parse `Retry-After`; on 429/503 tighten the host limiter
  (`SetLimit`) until the deadline; honor robots `Crawl-Delay` → `rate.Every(delay)`.
- The same `Fetcher` (and the same per-host limiter) serves both feed polls and article
  scrapes, so all traffic to one host shares one budget.

### 10.3 `parse` — feed parsing
- Wraps `gofeed` (auto-detects RSS/Atom/JSON Feed). Normalises into `ParsedFeed`/`ParsedEntry`,
  resolves relative URLs against the feed URL, surfaces any feed-supplied TTL hint, and
  computes a stable `GUID` (feed id, else `hash(url|title)`) and content `Hash`.

### 10.4 `extract` — full-content scrape
- Wraps `readeck/go-readability` **v2**. Given a fetched page, returns main-article HTML.
- Used only for `FetchFullContent` feeds (or entries with no usable content), in the scrape
  stage. Output is **sanitised before storage** like any other content.

### 10.5 `sanitize` — safe HTML & tracking mitigation
- bluemonday allowlist policy: permit semantic content tags; **strip** `<script>`, `<style>`,
  `<iframe>`, `<object>`, `<form>`, and all `on*` event-handler attributes.
- Tracking mitigation: strip known tracking query params (`utm_*`, `fbclid`, `gclid`, …)
  from links; drop 1×1 tracking-pixel images.
- When the image proxy is enabled (default), rewrite `<img src>`/`srcset` to signed proxy
  URLs (§10.6) so the browser never contacts origin servers.

### 10.6 `imgproxy` — image proxy (privacy; **enabled by default**)
A dedicated lever for the "minimise tracking" requirement: without it, images in entries
leak the reader's IP/User-Agent to origin and third-party tracker servers on render.

- **Endpoint:** `GET /img?u=<url>&s=<sig>`. `sig` is an **HMAC** over the URL keyed by a
  server secret — bfeed only proxies URLs it signed, so it is **never an open relay**.
- **Fetch path:** retrieves the image through the polite `Fetcher` (host limits/rate limits
  apply), with a strict timeout and max-bytes cap.
- **SSRF guard:** only `http`/`https`; resolve and **reject private, loopback, link-local,
  and metadata IPs**; enforce a `Content-Type: image/*` allowlist on the response.
- **Caching:** small on-disk (or bounded in-memory LRU) cache keyed by URL hash with a size
  cap and TTL, so repeated renders don't re-fetch. Serves with long client cache headers.
- **Signing key:** an HMAC secret resolved at startup — use `BFEED_IMAGE_PROXY_SECRET` if set
  (operator-managed); otherwise read it from the `app_settings` table, generating a random
  32-byte key with `crypto/rand` on first run and persisting it there. It lives in the same
  SQLite volume as the data, so it is stable across restarts. Rotating it invalidates
  signatures embedded in already-served pages (harmless — pages re-render on next load) but
  not the on-disk image cache (keyed by URL hash, not signature).
- **Config:** `BFEED_IMAGE_PROXY=on|off` (default **on**), `BFEED_IMAGE_PROXY_SECRET`
  (optional), cache size/dir, max image bytes.

### 10.7 `web` / 10.8 `api` / 10.9 `observability`
See §18, §17, §20.

## 11. Data model & schema

Per-user ownership: `feeds`, `entries`, `categories`, `api_tokens`, `sessions` all carry
`user_id`; deleting a user cascades all of it. **All tables `STRICT`. All timestamps are
`INTEGER` Unix seconds (UTC)** — smaller indexes, direct numeric range scans, no timezone
ambiguity. Booleans are `INTEGER 0/1` with `CHECK`. `foreign_keys=ON` and every child FK
column is indexed.

```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0,1)),
  entry_ttl_days INTEGER,                       -- NULL = use server default (§14)
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE categories (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  UNIQUE (user_id, title)
) STRICT;
CREATE INDEX idx_categories_user ON categories(user_id);

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
  fetch_full_content INTEGER NOT NULL DEFAULT 0 CHECK (fetch_full_content IN (0,1)),
  disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
  checked_at INTEGER,
  next_check_at INTEGER NOT NULL,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (user_id, feed_url)
) STRICT;
-- dispatch query: WHERE disabled=0 AND error_count<limit AND next_check_at<=? ORDER BY next_check_at
CREATE INDEX idx_feeds_due  ON feeds(next_check_at) WHERE disabled = 0;
CREATE INDEX idx_feeds_user ON feeds(user_id);

CREATE TABLE entries (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',             -- sanitised HTML
  summary TEXT NOT NULL DEFAULT '',             -- sanitised HTML
  published_at INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'unread' CHECK (status IN ('unread','read')),
  starred INTEGER NOT NULL DEFAULT 0 CHECK (starred IN (0,1)),
  read_at INTEGER,
  created_at INTEGER NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  UNIQUE (feed_id, guid)
) STRICT;
-- hottest query + keyset pagination (trailing id breaks published_at ties deterministically)
CREATE INDEX idx_entries_user_status_pub ON entries(user_id, status, published_at DESC, id DESC);
-- per-feed list; also serves as the entries.feed_id FK index
CREATE INDEX idx_entries_feed_pub ON entries(feed_id, published_at DESC);
-- partial indexes: small, cheap to maintain, used only when WHERE matches
CREATE INDEX idx_entries_starred  ON entries(user_id, published_at DESC) WHERE starred = 1;
CREATE INDEX idx_entries_readhist ON entries(user_id, read_at DESC, id DESC) WHERE read_at IS NOT NULL;
-- matches the TTL delete predicate exactly (read AND not starred), keeps cleanup a cheap scan
CREATE INDEX idx_entries_ttl ON entries(published_at) WHERE status = 'read' AND starred = 0;

CREATE TABLE tombstones (                        -- prevents re-creating deleted/expired entries
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  deleted_at INTEGER NOT NULL,
  PRIMARY KEY (feed_id, guid)
) STRICT, WITHOUT ROWID;                          -- composite key IS the lookup index; small rows
CREATE INDEX idx_tombstones_age ON tombstones(deleted_at);

CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL UNIQUE,                      -- SHA-256 of token; lookup by hash
  read_only INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0,1)),
  last_used_at INTEGER,
  expires_at INTEGER,
  created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,                            -- 256-bit random; opaque
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;                          -- text PK, small rows, exact-match lookups
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE app_settings (                       -- generated server secrets (e.g. image-proxy HMAC key)
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT, WITHOUT ROWID;                          -- small key/value store

-- FTS5 full-text index over entries (external content — no duplicate text copy)
CREATE VIRTUAL TABLE entries_fts USING fts5(
  title, content, content='entries', content_rowid='id', tokenize='unicode61'
);
CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;
CREATE TRIGGER entries_ad AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
END;
CREATE TRIGGER entries_au AFTER UPDATE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content) VALUES('delete', old.id, old.title, old.content);
  INSERT INTO entries_fts(rowid, title, content) VALUES (new.id, new.title, new.content);
END;
```

> Every query is scoped by `user_id`; never trust an id without the owning user. Pagination
> is **keyset** on `(published_at, id)`, never `OFFSET`. Verify plans with
> `EXPLAIN QUERY PLAN` — expect index use and **no** `USE TEMP B-TREE FOR ORDER BY`.

## 12. Adaptive feed-poll scheduling

bfeed uses a Miniflux-style **entry-frequency adaptive** interval: active feeds are polled
more often, quiet feeds less, all within `[min, max]` bounds.

**Scheduler** (one goroutine): on a short tick (`BFEED_POLL_TICK`, default **1m** — kept
**≪ min interval** so the adaptive minimum actually bites), selects a batch:

```sql
SELECT ... FROM feeds
WHERE disabled = 0 AND error_count < :error_limit AND next_check_at <= :now
ORDER BY next_check_at ASC LIMIT :batch;        -- BFEED_BATCH_SIZE, default 100
```

and sends feed jobs to the **feed-poll pool** (bounded channel; backpressure caps enqueue).

**Feed-poll worker** for each due feed:
1. Acquire global + per-host semaphore + per-host rate token (§10.2).
2. `Fetcher.Fetch` with stored ETag/Last-Modified.
   - **304** → no parse; reset `error_count`; reschedule (below). Done.
   - **2xx** → continue.
   - **error / 429 / 5xx** → record `last_error`, `error_count++`; reschedule with backoff. Done.
3. `FeedParser.Parse`.
4. Per parsed entry: skip if `(feed_id, guid)` tombstoned; **sanitise** content/summary.
5. `EntryStore.UpsertEntries` → returns newly-inserted entries (FTS stays in sync via triggers).
6. If `FetchFullContent`, **enqueue** the new entries to the article-scrape stage (§13) —
   the poll worker does **not** block on scraping.
7. Update feed metadata (title/site/desc, new ETag/Last-Modified), reset `error_count`.
8. **Reschedule** `next_check_at = now + interval` and release semaphores.

**Adaptive interval** (pure function in `schedule.go`, unit-tested with a fake `Clock`):

```
if weeklyCount <= 0:  interval = maxInterval                 // new/quiet feed
else:                 interval = week / (weeklyCount * factor)
interval = clamp(interval, minInterval, maxInterval)
interval = max(interval, feedTTL, retryAfter)                // honor server hints (may exceed max)
on error: interval = min(maxInterval, interval * 2^min(error_count, k)) + jitter  // graceful backoff
next_check_at = now + interval
```

- **`weeklyCount` is a spacing-based virtual count**, not `COUNT(*)`, so a burst (a new feed
  dumping hundreds of entries) doesn't pin it to the floor. `WeeklyEntryCount` SQL:
  ```sql
  SELECT COALESCE(CAST(CEIL(
    604800.0 / NULLIF((MAX(published_at) - MIN(published_at)) / NULLIF(COUNT(*) - 1, 0), 0)
  ) AS INTEGER), 0)
  FROM entries WHERE feed_id = ? AND published_at >= ? - 604800;   -- ? = now (unix s)
  ```
- **Graceful error backoff** replaces Miniflux's hard "stop after 3 errors": the interval
  doubles per consecutive error (capped at `maxInterval`), so a flapping feed slows down but
  **self-recovers** on the next success (which resets `error_count`). A separate hard
  `error_limit` (default high, e.g. 20) excludes hopelessly-broken feeds from dispatch until
  a manual or successful reset; we surface errors in the UI rather than silently disabling.
- Defaults: factor **1**, min **5m**, max **24h** (`BFEED_SCHED_*`).

Global concurrency = feed-poll worker count (`BFEED_FEED_WORKERS`, default **100** to meet the
concurrency requirement; per-host caps keep it polite). All steps honor `ctx` cancellation.

## 13. Content extraction (article scrape) — decoupled stage

Full-content scraping is a **separate worker pool + queue** from feed polling, so a large
backfill can never starve normal feed updates.

- Triggered for `FetchFullContent` feeds: step 6 of §12 enqueues newly-inserted entries.
- The **article-scrape pool** (`BFEED_SCRAPE_WORKERS`, default 20) drains the queue, sharing
  the **same per-host limiter** as feed polling (one budget per host).
- **Live entries are prioritised over historical backfill**, and a new feed's backfill is
  **capped per cycle** (`BFEED_BACKFILL_PER_HOST_PER_CYCLE`, default ~30) so hundreds of
  same-host articles drain over a few minutes at the polite rate instead of all at once —
  this is the fix for "a new feed with hundreds of entries blocks for ages."
- Per entry: `Fetcher.Fetch` the URL → `Extractor.Extract` → **`Sanitizer.Sanitize`** →
  `EntryStore.SetEntryContent`. Failures are logged and non-fatal; the entry keeps whatever
  the feed provided.

> **As built (iter 4):** the decoupling is realised with **DB-backed extraction state**, not an
> in-memory queue + second pool. Entries carry an `extract_state` (`none`/`pending`/`done`/
> `failed`) + `extract_attempts` + `next_extract_at`; a background `Scraper` sweep drains
> `pending` entries (own tick + bounded pool), freshest-first, capped per cycle (`BFEED_SCRAPE_
> BATCH`), sharing the one per-host `Fetcher` budget. Durable across restarts. Failure path:
> exponential backoff + jitter, terminal `failed` after `BFEED_SCRAPE_MAX_ATTEMPTS`. The
> `BFEED_BACKFILL_PER_HOST_PER_CYCLE` cap is **deferred** — the global per-cycle batch cap +
> shared per-host semaphore + freshest-first ordering cover the starvation concern. Full spec:
> `docs/superpowers/specs/2026-06-20-full-content-extraction-design.md`.

## 14. Retention, cleanup & tombstones

- **Cleaner** runs on an interval (`BFEED_CLEANUP_INTERVAL`, default daily).
- For each user, deletes entries that are **`status='read'` AND `starred=0`** AND
  `published_at < now - TTL`, writing a **tombstone** per deletion so re-polling can't
  resurrect them. TTL is **per-user** (`users.entry_ttl_days`), falling back to the server
  default (`BFEED_DEFAULT_ENTRY_TTL_DAYS`, default **365**).
- **Unread entries are never auto-deleted. Starred/saved entries are never auto-deleted** —
  regardless of age. Only read-and-unsaved old entries are cleaned. (Matches the partial
  index `idx_entries_ttl`.)
- Deleting a feed cascades its entries + FTS rows and writes tombstones.
- **Tombstone pruning** (`PruneTombstones`): tombstones older than a window longer than any
  feed would re-list (e.g. 2× the max TTL) are removed to bound growth, on the assumption
  feeds don't re-publish items that old.
- After large deletes the cleaner runs `PRAGMA wal_checkpoint(TRUNCATE)` + `PRAGMA optimize`.

## 15. Full-text search

- Backed by **SQLite FTS5** (`entries_fts`, external-content), kept in sync with `entries`
  by the canonical triggers (delete via the `'delete'` command + old values, §11).
- `SearchService` maps the user query to an FTS5 `MATCH`, intersects with `user_id` scope and
  any `EntryFilter`, orders by relevance or recency, paginates via keyset cursor.
- Tombstoned/deleted entries are pruned from FTS by the delete trigger, so they never appear.
- `unicode61` tokenizer (diacritic folding); add `porter` for stemming if wanted later.

## 16. Authentication & authorization

- **Web sessions:** username + password → argon2id verify → random 256-bit session id stored
  in DB and in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Validated per request; expired
  sessions rejected and reaped by `DeleteExpiredSessions`.
- **CSRF:** synchroniser token in a cookie + form/header; htmx configured to send it on
  mutating requests. GET is safe; all state-changing web routes require a valid token.
- **API:** `Authorization: Bearer <token>`. Middleware SHA-256s the token, looks up by hash,
  checks expiry, resolves the owning user, updates `LastUsedAt`. `ReadOnly` tokens may only
  use safe methods.
- **Tokens** are per-user, act as that user, shown in plaintext once at creation (only the
  hash is stored). Users manage their own tokens.
- **Admin role:** `is_admin` users access `/admin` (web) and user-management API routes,
  enforced by an admin-guard middleware.
- **Bootstrap:** on first start with zero users, create an admin from
  `BFEED_ADMIN_USERNAME` / `BFEED_ADMIN_PASSWORD`. Self-registration is not supported.

## 17. REST API surface (v1)

JSON, versioned under `/v1`, bearer-authenticated. Representative routes:

```
GET    /v1/feeds            list          POST  /v1/feeds        subscribe
GET    /v1/feeds/{id}       detail        PUT   /v1/feeds/{id}   edit (category, full-content, disabled)
DELETE /v1/feeds/{id}       delete (+entries,+tombstones)
GET    /v1/feeds/{id}/entries             POST  /v1/feeds/{id}/refresh   force refresh now

GET/POST /v1/categories     PUT/DELETE /v1/categories/{id}

GET    /v1/entries          list (status, starred, feed_id, category_id, cursor)
GET    /v1/entries/{id}     PUT /v1/entries/{id} (status/starred)   DELETE /v1/entries/{id} (+tombstone)
POST   /v1/entries/bulk     bulk status/starred over ids or a filter

GET    /v1/search?q=...     full-text search
GET    /v1/export           OPML export    POST /v1/import   OPML import
GET/POST /v1/tokens         DELETE /v1/tokens/{id}           manage own tokens

GET/POST /v1/users          PUT/DELETE /v1/users/{id}        admin only

GET    /healthz  /readyz    liveness/readiness (unauthenticated)
GET    /metrics             Prometheus (bind/guard per config)
GET    /img?u=..&s=..       image proxy (signed; §10.6)
```

Errors return `{ "error": { "code", "message" } }`; core sentinels map to status
(`ErrNotFound`→404, `ErrConflict`→409, `ErrUnauthorized`→401, `ErrValidation`→422).

## 18. Web UI

- Server-rendered `html/template`, progressively enhanced with **htmx**. No SPA framework.
- Single-column, content-first, serif body text. Light/dark/system theme via a
  `prefers-color-scheme` baseline plus a user toggle (persisted in a cookie).
- htmx drives: mark read/unread, star, bulk actions, keyset pagination/infinite scroll,
  refresh — each returns an HTML fragment swapped in place. Full-page loads stay small.
- Views: unread list (home), all feeds, single feed, categories, starred, history, search,
  settings (incl. per-user entry TTL), login/logout, admin/users.
- Entry HTML is rendered already-sanitised; `<img>` sources point at the image proxy.
- **Add-to-home-screen:** `manifest.webmanifest`, app icons, and `apple-touch-icon` + meta
  tags so iOS can install bfeed to the home screen with a proper icon. **No service worker
  and no offline mode** — iOS add-to-home-screen needs only the manifest, icons, and meta
  tags, which keeps JS minimal as required.
- Static assets (CSS, htmx, icons, manifest) embedded via `embed.FS`, served with cache
  headers; no build step, no bundler.

## 19. OPML import/export

- **Export:** OPML 2.0 with categories as `<outline>` groups. Plaintext, no credentials.
- **Import:** parse OPML, create missing categories, subscribe to each feed (dedupe per user
  by `feed_url`), skip duplicates. Best-effort: malformed entries are reported, not fatal.

## 20. Observability

**slog** everywhere: leveled, structured (JSON in prod, text in dev — config). High-cardinality
detail goes to logs: per-feed poll timing, per-feed errors with URL/status, request ids,
user/feed ids.

**Prometheus** (low-cardinality only). For the database, **reuse only the built-in
`collectors.NewDBStatsCollector(db, "bfeed")`** (zero new deps) → `go_sql_*` connection-pool
gauges (max/open/in-use/idle conns, `wait_count`, `wait_duration`). No hand-rolled DB
collectors. Two observability questions therefore move off Prometheus, consistent with the
requirement to use logs for high-cardinality/low-level detail:
- *How long do DB operations take?* — slow queries are timed and logged via slog;
  `go_sql_wait_*` surfaces pool contention.
- *How big is the database?* — emitted as a periodic slog line (also visible via
  container/host filesystem metrics); not a custom gauge.

**Errors are a single counter with closed-enum labels** (Prometheus best practice — avoid
metric-name proliferation): `bfeed_errors_total{component, reason}` where
`component ∈ {feed_poll, article_scrape, db, http_server, auth, image_proxy}` and
`reason ∈ {timeout, dns, tls, http_4xx, http_5xx, rate_limited, parse, internal}`. Raw error
strings are bucketed into these in code — **never** label by feed/host/url/user (cardinality
bomb). Paired attempt counters enable error-ratio queries.

| Question | Metric |
|---|---|
| How many users? | `bfeed_users_total` (gauge) |
| Subscriptions per user? | `bfeed_feeds_total{user}` (gauge; safe at O(1) users) |
| Entries total? | `bfeed_entries_total` (gauge) |
| Entries per feed? | not labelled (high cardinality) — admin view / logs |
| Time to update feeds? / per-feed poll time? | `bfeed_feed_poll_duration_seconds` (histogram); individual values → logs |
| Article scrape time? | `bfeed_article_scrape_duration_seconds` (histogram) |
| Queued / in progress? | `bfeed_poll_queue_depth`, `bfeed_poll_inflight`, `bfeed_scrape_queue_depth`, `bfeed_scrape_inflight` (gauges) |
| Feed-poll attempts (denominator)? | `bfeed_feed_polls_total{result}` (counter) |
| Errors (all kinds)? | `bfeed_errors_total{component, reason}` (counter) |
| Database size? | periodic slog line + host/container disk metrics (no custom gauge) |
| DB operation latency? | slog slow-query log + `go_sql_wait_*` (pool contention) |

HTTP: `bfeed_http_requests_total{route,method,status}`, `bfeed_http_request_duration_seconds{route}`.

## 21. Configuration (12-factor)

Environment variables, validated at startup, with sensible defaults:

```
# server / storage
BFEED_LISTEN_ADDR              :8080
BFEED_BASE_URL                 external URL (cookies, OPML, image-proxy signing, UA)
BFEED_DATABASE_PATH            ./bfeed.db
BFEED_METRICS_ADDR             optional separate bind for /metrics
BFEED_LOG_LEVEL                info        BFEED_LOG_FORMAT   json
# scheduling (adaptive)
BFEED_POLL_TICK                1m          # global scheduler tick (≪ min interval)
BFEED_BATCH_SIZE               100         # feeds dispatched per tick
BFEED_FEED_WORKERS             100         # feed-poll pool (meets ≥100 concurrency requirement)
BFEED_SCHED_MIN_INTERVAL       5m
BFEED_SCHED_MAX_INTERVAL       24h
BFEED_SCHED_FACTOR             1
BFEED_FEED_ERROR_LIMIT         20          # exclude from dispatch after N consecutive errors
# politeness
BFEED_HOST_CONCURRENCY         3           # max concurrent requests per host
BFEED_HOST_RATE_PER_SEC        1           # per-host token-bucket rate
BFEED_HOST_BURST               3
# article scraping (full content)
BFEED_SCRAPE_WORKERS           20
BFEED_SCRAPE_TICK              1m
BFEED_SCRAPE_BATCH             50
BFEED_SCRAPE_MAX_ATTEMPTS      3
BFEED_BACKFILL_PER_HOST_PER_CYCLE  30          # deferred
# retention
BFEED_DEFAULT_ENTRY_TTL_DAYS   365         # per-user override stored in DB
BFEED_CLEANUP_INTERVAL         24h
# auth
BFEED_SESSION_TTL              720h        # 30d
BFEED_ADMIN_USERNAME / BFEED_ADMIN_PASSWORD   # bootstrap admin, first run only
# privacy
BFEED_IMAGE_PROXY              on          # default ON
BFEED_IMAGE_PROXY_SECRET                   # optional HMAC key; generated + persisted if unset
BFEED_IMAGE_CACHE_DIR / BFEED_IMAGE_CACHE_MAX_BYTES / BFEED_IMAGE_MAX_BYTES
```

## 22. Command-line interface

One binary, multiple subcommands; dispatch in `cmd/bfeed` using stdlib `flag` (boring — only
a handful of commands, no need for a framework). Config is read from the environment (§21);
flags override where useful. The distroless/static image has no shell, so admin and health
operations are subcommands, not shell scripts.

```
bfeed serve                  run the HTTP server + background poller/cleaner (default if omitted)
bfeed migrate <up|down|status>   manage schema migrations (goose); `serve` also auto-migrates on boot
bfeed user <create|list|set-password|delete> --username NAME [--admin]
                             manage users (alternative to env bootstrap); password from prompt/stdin
bfeed token <create|list|revoke> --username NAME [--name LABEL] [--read-only]
                             manage a user's API tokens; `create` prints the token once
bfeed import <file.opml> --username NAME       OPML import
bfeed export --username NAME [-o file.opml]    OPML export
bfeed healthcheck            probe local /healthz and exit 0/1 — for the container HEALTHCHECK
bfeed version                version, git commit, build date
```

- `serve` is the default when no subcommand is given.
- Every command shares one config-load → DB-open → migrate path, then acts on the single
  SQLite file. Offline admin commands rely on WAL + `busy_timeout` to coexist with a running
  `serve`. These commands are thin CLI adapters over the same core services (§9) — no logic
  lives in `cmd`.
- `healthcheck` exists because distroless ships no `curl`/`wget`; the container `HEALTHCHECK`
  invokes `bfeed healthcheck`.

## 23. Concurrency & lifecycle

- `cmd/bfeed` builds a root `context.Context` cancelled on SIGINT/SIGTERM.
- Startup: load+validate config → open SQLite (pragmas, writer pool) → run migrations (goose)
  → bootstrap admin if needed → construct adapters → construct services → start the poll
  scheduler, feed-poll pool, article-scrape pool, and cleaner (goroutines) → start HTTP server.
- Shutdown: cancel context → HTTP `Shutdown` (drain in-flight) → scheduler stops enqueuing →
  pools finish/abort current fetch on ctx → `PRAGMA optimize` → close DB. Use
  `errgroup`/`sync.WaitGroup` to wait for clean drain within a timeout.

## 24. Error handling conventions

- Core defines sentinel errors (`ErrNotFound`, `ErrConflict`, `ErrUnauthorized`,
  `ErrValidation`). Adapters wrap driver errors with `%w` and map to sentinels.
- Services return errors up; driving adapters translate to HTTP status / HTML.
- Background workers log and continue — one bad feed never halts a pool.
- No silent failures: every swallowed/decided-non-fatal error is logged with context and, if
  operationally relevant, counted in `bfeed_errors_total`.

## 25. Testing strategy (TDD: Red/Green/Refactor)

- **Core services:** unit-tested with in-memory fakes implementing the port interfaces and a
  **fake `Clock`** — fast, deterministic. Invariant tests live here, including the adaptive
  interval math (`schedule.go`) and backoff.
- **`store/sqlite`:** integration-tested against a real SQLite file/`:memory:` — sqlc queries,
  migrations, STRICT constraints, FTS sync triggers, cascade deletes, tombstone behaviour,
  keyset pagination, `EXPLAIN QUERY PLAN` checks on the hot queries.
- **`fetch`:** `httptest.Server` — conditional GET (304), Retry-After, per-host concurrency +
  rate limiting, timeouts.
- **`parse`/`extract`/`sanitize`:** golden-file tests over fixture feeds/HTML, incl. malicious
  HTML asserting active content/trackers are stripped and img sources are proxied.
- **`imgproxy`:** signature verification, SSRF rejection of private IPs, content-type allowlist.
- **`api`/`web`:** `httptest` handler tests — auth, CSRF, scoping, status mapping, fragments.
- **End-to-end smoke:** wire real components, subscribe to a local test feed, assert entries
  appear, search works, mark-read/star/delete behave, adaptive reschedule advances.

## 26. Deployment

- Single static binary (`CGO_ENABLED=0`), templates/assets/migrations embedded — ship nothing
  but the SQLite file.
- **Tiny distroless** container (`gcr.io/distroless/static`), multi-arch (amd64 + arm64 for
  Pi), built and pushed to **GHCR** via GitHub Actions.
- Runs as non-root; data dir is a mounted volume holding `bfeed.db` (+ WAL/SHM) and the image
  cache. Restart-safe (migrations idempotent, scheduler self-heals from `next_check_at`).

## 27. Invariants (the contract)

These hold across all sessions. Tests must defend them.

**Safety**
1. HTML is **always** sanitised before persistence. Raw feed/extracted HTML never reaches the DB.
2. Sanitised HTML contains no `<script>`, event handlers, or active embeds.
3. The image proxy is **never an open relay**: it serves only HMAC-signed URLs, rejects
   private/loopback/link-local/metadata IPs, and enforces an `image/*` content-type allowlist.

**Politeness**
4. Feed fetches always attempt conditional GET when validators are stored.
5. Per-host concurrency never exceeds the cap **and** per-host request rate never exceeds the
   token-bucket limit; global concurrency never exceeds the pool size. Feed polls and article
   scrapes to the same host share one budget.
6. `Retry-After`/429/503 and robots `Crawl-Delay` are honoured; error reschedule uses
   exponential backoff with jitter (no aggressive immediate retries).
7. Article-scrape backfill is capped per host per cycle and never starves feed polling.

**Data integrity**
8. A tombstoned `(feed_id, guid)` is never re-created by polling.
9. Auto-cleanup deletes **only** entries that are read **and** not starred **and** older than
   the (per-user) TTL. **Unread entries and starred entries are never auto-deleted.**
10. Deleting a feed removes its entries and FTS rows and leaves tombstones.
11. `entries_fts` always reflects current `entries` (triggers; delete uses old values).
12. `(feed_id, guid)` is unique; re-fetched entries upsert, never duplicate.
13. The adaptive interval is clamped to `[min, max]` and error backoff is capped at `max`;
    only server-mandated delays (feed TTL, `Retry-After`) may push `next_check_at` beyond
    `max`. It is never scheduled below `min`.

**Authorization & isolation**
14. Every data query is scoped by `user_id`; no cross-user read/write via id alone.
15. Every API request resolves to a user via a hashed token; read-only tokens cannot mutate.
16. Admin-only routes require `is_admin`.

**Persistence**
17. All tables are `STRICT`; all timestamps are INTEGER Unix-seconds UTC; booleans are 0/1
    with CHECK; `foreign_keys=ON` on every connection and every child FK column is indexed.
18. Writes are serialised (single writer connection); list/search pagination is keyset, never OFFSET.

**Architecture**
19. `internal/core` imports no adapter package; dependencies point inward.
20. Interfaces are declared by the consuming core; adapters depend on core, not vice-versa.
21. Time-dependent logic uses the injected `Clock`, never `time.Now()` directly.

## 28. Open questions / future

- **License** (requirements: TBD) — pick an OSI license (e.g. AGPL-3.0 or MIT) before release.
- **Per-feed interval override** — adaptivity is global today; a per-feed min/max pin could be
  added if a feed needs special cadence.
- **Read/write connection split** — adopt only if single-writer + `busy_timeout` proves
  insufficient under load.
- **Search stemming** — the default `unicode61` tokenizer matches only exact word forms
  (after case/accent folding), so a search for `run` will not match `running` or `runs`.
  FTS5's `porter` tokenizer stems words to a common root at both index and query time so
  inflected forms match each other — higher recall, traded against some precision
  (`universe`/`university` collide) and the ability to match an exact form. Default stays
  `unicode61`; the tokenizer is a config option to flip to `porter` if search feels too literal.

## 29. Decision log (deltas from first draft)

- Per-user feeds (Miniflux model); fetch-time per-feed content scrape; sqlc; stdlib `net/http`.
- argon2id over bcrypt; `Clock` kept as the time port name.
- Switched extraction lib to maintained `readeck/go-readability` (go-shiori archived).
- Added Miniflux-style adaptive refresh with spacing-based weekly count + graceful backoff.
- Per-host concurrency raised to 3 + token-bucket rate limit; feed poll and article scrape
  split into separate pools with capped backfill (fixes slow large-feed subscribe).
- Timestamps → INTEGER epoch; STRICT tables; partial/covering indexes; keyset pagination;
  single-writer pool; canonical FTS5 triggers.
- TTL cleanup restricted to **read AND not-saved** entries; default **365d**, **per-user** configurable.
- Image proxy promoted from an aside to a fully-scoped feature, **default ON**.
- Dropped invented service-worker/offline reading.
- DB metrics: errors collapsed into one `bfeed_errors_total{component,reason}`.
- **R2:** readability → **v2** (greenfield, latest); `obs` package renamed `observability`;
  dropped all custom DB collectors (`go_sql_*` only — DB size/op-latency routed to logs +
  host metrics); image-proxy HMAC key resolved from `BFEED_IMAGE_PROXY_SECRET` else generated
  once and persisted in the new `app_settings` table; added CLI design (§22); clarified the
  search-stemming trade-off.
- **History (iter 2):** `/history` lists read entries by `read_at` (keyset `(read_at,id)`);
  `idx_entries_readhist` gains a trailing `id DESC` so the keyset order needs no temp B-tree.
  Entry rows now render published time as a relative string ("2h ago") across all views.
- **Categories (iter 3):** `categories` table + nullable `feeds.category_id` (ON DELETE SET NULL);
  aggregated per-category entry stream filters via a `feeds` JOIN in `ListEntries`, kept sort-free
  by a new `idx_entries_user_pub (user_id, published_at DESC, id DESC)`; `/categories` index shows
  unread counts incl. an uncategorised bucket; assignment at subscribe time and via `SetFeedCategory`.
- **Search (iter 3):** FTS5 external-content `entries_fts` over **title, content, summary**
  (design §11 listed title+content; summary added so description-only RSS feeds are body-searchable),
  synced by triggers using **`AFTER UPDATE OF title, content, summary`** so read/star toggles don't
  churn the index. Results are bm25-ranked (`ORDER BY rank`) and **capped at 50 with no pagination**
  this iteration (relevance has no stable keyset; rank-keyset deferred). A per-token quote-and-AND
  MATCH builder (prefix-* on the last token) keeps arbitrary input from raising FTS5 syntax errors;
  operator syntax is intentionally not exposed. FTS5 MATCH construction lives in the sqlite adapter,
  keeping core FTS5-agnostic.
- **Full-content extraction (iter 4):** opt-in per feed (`feeds.fetch_full_content`); extraction
  realised via **DB-backed state** on `entries` (`extract_state`/`extract_attempts`/`next_extract_at`
  + partial `idx_entries_pending`), **not** the §13 in-memory queue+pool — durable, restart-safe,
  same starvation guarantees. New `Extractor` port (`extract/`, `readeck/go-readability/v2`) and
  `EntryScraper`/`ScrapeService`/`Scraper` (mirrors `FeedPoller`/`FeedService`/`Poller`); scrapes
  reuse the one `Fetcher` per-host budget. Replaces feed `content` on success; keeps feed content
  on failure (bounded exponential backoff → terminal `failed`). Enabling a feed backfills **all**
  its existing entries. Config `BFEED_SCRAPE_{WORKERS,TICK,BATCH,MAX_ATTEMPTS}`;
  `BFEED_BACKFILL_PER_HOST_PER_CYCLE` deferred. Full spec:
  `docs/superpowers/specs/2026-06-20-full-content-extraction-design.md`.

## 30. Research basis & sources

Key external references behind the researched decisions (verified during design):

- **Adaptive scheduler** — Miniflux v2 source (`internal/model/feed.go` `ScheduleNextCheck`,
  `internal/storage/feed.go` `WeeklyFeedEntryCount`, `internal/config/options.go` defaults);
  https://miniflux.app/docs/configuration.html
- **SQLite schema/PRAGMAs** — sqlite.org: STRICT tables, datatypes/affinity, WITHOUT ROWID,
  query planner, partial indexes, foreign keys, WAL, FTS5, pragma reference; River's Go
  SQLite pooling guidance (single-writer pool).
- **Polite polling** — `golang.org/x/time/rate`, Go wiki RateLimiting, Scrapy AutoThrottle /
  per-domain settings, web-crawler politeness (token bucket + per-host cap, 429/Retry-After),
  Miniflux `WORKER_POOL_SIZE`/`BATCH_SIZE`/`POLLING_LIMIT_PER_HOST`.
- **Metrics** — Prometheus instrumentation & naming best practices (labels over metric-name
  proliferation; bounded cardinality; pair failures with attempt totals); client_golang
  `collectors.NewDBStatsCollector`.
- **Extraction lib** — go-shiori/go-readability archived 2025-12-30; maintained fork at
  codeberg.org/readeck/go-readability (MIT, pure Go).
```
