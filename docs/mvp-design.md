# bfeed — MVP Design

> Status: MVP scope for the **first shippable iteration** of bfeed.
> This document is **subordinate to** [`design.md`](./design.md): the full design is the
> north star, this is the tightly-scoped subset built first so it can be used and iterated on.
> Every "deferred" item below is fully specified in `design.md` and lands in a later iteration
> as an **additive** change (new tables/columns/packages), never a rewrite.

---

## 1. MVP goal

A **personal daily-driver feed reader** Ben can run behind his Tailscale tailnet and use from
his iPhone on day one. The day-one loop:

```
add feed URL  →  background poll (polite)  →  unread list
              →  open entry  →  mark read / star
```

Everything not on that loop is deferred. Leanest possible first ship; iterate from there.

## 2. Scope decisions (from scoping interview)

| Area | MVP decision | Deferred to later iteration |
|---|---|---|
| Users / auth | **No login.** Tailnet is the security boundary. Implicit `user_id = 1`. | Sessions, CSRF, argon2id, multi-user, admin (`design.md` §16) |
| Polling | **Fixed interval** + politeness (conditional GET, per-host concurrency cap, exp backoff), one bounded worker pool | Adaptive scheduler, weekly-count, token-bucket rate limiter, scrape pool (§12–13) |
| Content | Show feed-provided content, **sanitised** | Full-content scrape / Readability (§13), image proxy (§10.6) |
| Search | FTS5 full-text search — shipped iter 3 (see `docs/superpowers/specs/2026-06-20-fts5-search-design.md`) | Porter stemming (§28) |
| Organisation | Flat feed list | Categories (§9.1) — shipped iter 3 (see `docs/superpowers/specs/2026-06-20-feed-categories-design.md`) |
| Retention | **Tombstones on delete** (correctness). No TTL cleaner. | TTL cleaner, prune, WAL maintenance job (§14) |
| API | — | REST API + bearer tokens (§17) |
| Data portability | — | OPML import/export (§19) |
| Mobile install | — | PWA add-to-home (manifest/icons) (§18) |
| Theme | — | Light/dark/system toggle (§18) |
| Reading extras | — | Bulk mark-all-read (§9.2) |
| Observability | **slog only** | Prometheus metrics, `/metrics` (§20) |

## 3. What's IN (the MVP feature set)

- Single self-contained Go binary, single SQLite file. Pure Go (`CGO_ENABLED=0`).
- **Subscribe** to a feed: accept a URL, fetch it, parse as feed; if the URL returns HTML,
  best-effort discover the feed via `<link rel="alternate" type="application/(rss|atom)+xml">`
  and use the first match. One immediate fetch populates title + entries and sets first
  `next_check_at`. Dedupe per the single user by `feed_url`.
- **Parse** RSS, Atom, and JSON Feed (`gofeed` — all three free in one API).
- **Sanitise** all feed HTML with `bluemonday` **before persistence** (strip `<script>`,
  `<style>`, `<iframe>`, `<object>`, `<form>`, all `on*` handlers). Strip known tracking query
  params (`utm_*`, `fbclid`, …) from links and drop 1×1 tracking-pixel images. **Raw HTML
  never reaches the DB.** (Image-proxy rewrite is deferred; images load from origin in the MVP.)
- **Store** entries; upsert by `(feed_id, guid)` (GUID = feed id, else `hash(url|title)`);
  content hash detects in-place edits. **Tombstone** `(feed_id, guid)` on single-entry delete
  (and TTL expiry, later) so re-polling the same feed can't resurrect it. Deleting a whole feed
  cascades its entries *and* their tombstones away (a re-subscribe gets a fresh `feed_id`, so
  per-feed tombstones would not apply to it).
- **Background poller** — fixed interval, one bounded worker pool, conditional GET (304 → no
  reparse), per-host concurrency semaphore, exponential backoff + jitter on error.
- **Web UI** — server-rendered `html/template` + htmx; mobile-first, single-column, serif body.
  Views: unread list (home), all-feeds list, single-feed entries, single-entry read view.
  Actions (htmx fragments): mark read/unread, star/unstar, delete entry, delete feed, subscribe,
  force-refresh-now, keyset "load more".
- **slog** structured logging throughout (JSON in prod, text in dev).
- 12-factor env config; distroless container; `bfeed healthcheck` for container HEALTHCHECK.

## 4. Architecture

Same **ports-and-adapters** layout as `design.md` §5, trimmed to the packages the MVP touches.
Dependencies point inward; `internal/core` imports no adapter; interfaces are consumer-owned.

```
cmd/bfeed/            composition root: config, wiring, lifecycle, CLI dispatch
internal/
  core/
    types.go          User, Feed, Entry, Tombstone
    ports.go          Store (+ FeedStore, EntryStore), Fetcher, FeedParser, Sanitizer, Clock
    errors.go         ErrNotFound, ErrConflict, ErrValidation
    feed.go           FeedService  (subscribe, list, delete, refresh)
    entry.go          EntryService (list, get, mark read/unread, star, delete)
    poller.go         Poller: fixed-interval scheduler + bounded worker pool
  store/sqlite/       Store impl: sqlc, queries/, migrations/ (embed.FS), pragmas, single-writer pool
  fetch/              Fetcher: polite conditional HTTP client, per-host semaphore
  parse/              FeedParser: gofeed adapter → ParsedFeed; feed discovery
  sanitize/           Sanitizer: bluemonday policy + tracking-param/pixel stripping
  web/                HTML handlers, templates (embed.FS), static assets (embed.FS)
  observability/      slog setup
  clock/              real Clock (time.Now); fake clock in tests
```

**Dropped vs full design:** `api/`, `extract/`, `imgproxy/`, `search` service, `cleaner`,
user/auth/session service, `categories`. Each is an additive package later.

## 5. Domain types (`internal/core`)

`ID` is `int64`. Times are Go `time.Time` (UTC), persisted as INTEGER Unix seconds.

```go
type ID int64

// One implicit row (id=1) seeded by migration; carries the FK target so multi-user
// is an additive change later. No password/admin fields in the MVP.
type User struct {
    ID        ID
    Username  string
    CreatedAt time.Time
}

type Feed struct {
    ID           ID
    UserID       ID
    FeedURL      string     // canonical feed URL (unique per user)
    SiteURL      string
    Title        string
    Description  string
    ETag         string     // conditional GET
    LastModified string     // conditional GET
    Disabled     bool       // user-disabled
    CheckedAt    *time.Time // last poll attempt
    NextCheckAt  time.Time  // scheduler key
    ErrorCount   int        // consecutive errors; drives backoff; resets on success
    LastError    string
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type EntryStatus string
const (
    StatusUnread EntryStatus = "unread"
    StatusRead   EntryStatus = "read"
)

type Entry struct {
    ID          ID
    UserID      ID        // denormalised from feed; powers the hot index
    FeedID      ID
    GUID        string
    URL         string
    Title       string
    Author      string
    Content     string    // SANITISED HTML (never raw)
    Summary     string    // SANITISED HTML
    PublishedAt time.Time
    Status      EntryStatus
    Starred     bool
    ReadAt      *time.Time
    CreatedAt   time.Time
    Hash        string    // content hash; drives upsert
}

type Tombstone struct {
    FeedID    ID
    GUID      string
    DeletedAt time.Time
}
```

## 6. Core interfaces (consumer-owned ports)

Trimmed to MVP needs. Every method takes `context.Context`.

```go
type Clock interface { Now() time.Time }

type Fetcher interface {
    Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error)
}
type FetchRequest  struct { URL, ETag, LastModified string }
type FetchResponse struct {
    Status       int
    NotModified  bool          // HTTP 304
    Body         []byte
    ContentType  string
    ETag         string
    LastModified string
    RetryAfter   time.Duration // parsed from Retry-After on 429/503
}

type FeedParser interface {
    Parse(data []byte, feedURL string) (*ParsedFeed, error)
    // Discover scans an HTML page for feed <link> hrefs (best-effort, returns absolute URLs).
    Discover(data []byte, pageURL string) ([]string, error)
}
type ParsedFeed  struct { Title, SiteURL, Description string; Entries []ParsedEntry }
type ParsedEntry struct {
    GUID, URL, Title, Author string
    Content, Summary         string // RAW; sanitise before persistence
    PublishedAt              time.Time
}

type Sanitizer interface { Sanitize(html, baseURL string) string }

type Store interface { FeedStore; EntryStore }

type FeedStore interface {
    CreateFeed(ctx context.Context, f *Feed) (ID, error)              // unique (user_id, feed_url)
    GetFeed(ctx context.Context, userID, feedID ID) (*Feed, error)
    ListFeeds(ctx context.Context, userID ID) ([]*Feed, error)
    ListDueFeeds(ctx context.Context, now time.Time, limit int) ([]*Feed, error) // next_check_at<=now, !disabled
    UpdateFeed(ctx context.Context, f *Feed) error
    DeleteFeed(ctx context.Context, userID, feedID ID) error          // cascade entries + tombstones (writes none)
}

type EntryStore interface {
    UpsertEntries(ctx context.Context, feedID ID, entries []*Entry) (inserted []*Entry, err error) // skips tombstoned
    GetEntry(ctx context.Context, userID, entryID ID) (*Entry, error)
    ListEntries(ctx context.Context, userID ID, f EntryFilter) ([]*Entry, *Cursor, error)
    SetStatus(ctx context.Context, userID ID, ids []ID, s EntryStatus) error
    SetStarred(ctx context.Context, userID ID, ids []ID, starred bool) error
    DeleteEntry(ctx context.Context, userID, entryID ID) error        // writes tombstone
}

// EntryFilter: zero value = all entries for the user. Keyset pagination on (published_at, id).
type EntryFilter struct {
    FeedID  *ID
    Status  *EntryStatus
    Starred *bool
    Limit   int
    Cursor  *Cursor
}
```

## 7. Core services

Plain structs, constructor DI, each takes a `*slog.Logger`. All business logic + invariant
enforcement lives here; web handlers are dumb translation.

- **FeedService** — subscribe (discover + validate + dedupe + one immediate fetch + set first
  `NextCheckAt`), list, delete (cascade + tombstones), force-refresh-now.
- **EntryService** — list/browse (by feed, status, starred), get one, mark read/unread, star,
  delete one (→ tombstone). Read/star state is per (single) user.
- **Poller** — fixed-interval scheduler + one bounded worker pool. See §9.

## 8. Schema (`store/sqlite`, goose migrations, STRICT)

All tables `STRICT`; all timestamps INTEGER Unix seconds UTC; booleans INTEGER 0/1 with CHECK;
`foreign_keys=ON`; every child FK column indexed. Connection PRAGMAs and single-writer pool
exactly as `design.md` §10.1 (`journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`,
`busy_timeout=5000`, `temp_store=MEMORY`, `cache_size=-8000`, `SetMaxOpenConns(1)`).

```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
) STRICT;
-- migration seeds the single row: INSERT INTO users(id, username, created_at) VALUES (1, 'ben', ...);

CREATE TABLE feeds (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_url TEXT NOT NULL,
  site_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
  checked_at INTEGER,
  next_check_at INTEGER NOT NULL,
  error_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (user_id, feed_url)
) STRICT;
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
  content TEXT NOT NULL DEFAULT '',     -- sanitised HTML
  summary TEXT NOT NULL DEFAULT '',     -- sanitised HTML
  published_at INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'unread' CHECK (status IN ('unread','read')),
  starred INTEGER NOT NULL DEFAULT 0 CHECK (starred IN (0,1)),
  read_at INTEGER,
  created_at INTEGER NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  UNIQUE (feed_id, guid)
) STRICT;
CREATE INDEX idx_entries_user_status_pub ON entries(user_id, status, published_at DESC, id DESC);
CREATE INDEX idx_entries_feed_pub        ON entries(feed_id, published_at DESC);
CREATE INDEX idx_entries_starred         ON entries(user_id, published_at DESC) WHERE starred = 1;

CREATE TABLE tombstones (
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  guid TEXT NOT NULL,
  deleted_at INTEGER NOT NULL,
  PRIMARY KEY (feed_id, guid)
) STRICT, WITHOUT ROWID;
```

> Deferred features add tables/columns additively: `categories` + `feeds.category_id`,
> `api_tokens`, `sessions`, `app_settings`, `entries_fts` (+ triggers), `users.password_hash`,
> `users.is_admin`, `users.entry_ttl_days`, `feeds.fetch_full_content`, `idx_entries_ttl`.
> None require rewriting MVP tables.

Queries authored as SQL, compiled by **sqlc**. Pagination is **keyset** on `(published_at, id)`,
never OFFSET. Driver errors mapped to core sentinels.

## 9. Poller (fixed interval)

> **Superseded in iter 6** by adaptive scheduling — the fixed `BFEED_POLL_INTERVAL`
> below is replaced by `BFEED_SCHED_MIN_INTERVAL`/`BFEED_SCHED_MAX_INTERVAL`/
> `BFEED_SCHED_FACTOR`. See `docs/design.md` §12 for the authoritative behaviour.

One scheduler goroutine on a tick (`BFEED_POLL_TICK`, default **1m**) selects due feeds:

```sql
SELECT ... FROM feeds
WHERE disabled = 0 AND next_check_at <= :now
ORDER BY next_check_at ASC LIMIT :batch;
```

and sends jobs to a bounded worker pool (`BFEED_FEED_WORKERS`, default **20**). Per due feed:

1. Acquire global + per-host semaphore (`fetch` adapter, §10).
2. `Fetcher.Fetch` with stored ETag/Last-Modified.
   - **304** → reset `error_count`; reschedule; done.
   - **2xx** → continue.
   - **error / 429 / 5xx** → record `last_error`, `error_count++`; reschedule with backoff; done.
3. `FeedParser.Parse`.
4. Per entry: skip if `(feed_id, guid)` tombstoned; **sanitise** content/summary.
5. `EntryStore.UpsertEntries`.
6. Update feed metadata (title/site/desc, new ETag/Last-Modified); reset `error_count`.
7. Reschedule:

```
next_check_at = now + BFEED_POLL_INTERVAL            // fixed, default 15m
on error: next_check_at = now + min(maxBackoff, interval * 2^min(error_count, k)) + jitter
```

Defaults: `BFEED_POLL_INTERVAL` **15m**, backoff cap **24h**. All steps honour `ctx`.
(The adaptive interval math from `design.md` §12 replaces step 7 in a later iteration.)

## 10. Fetch politeness (`fetch`)

- Single shared `*http.Client`: sane timeouts, redirect cap, capped response body size,
  `bfeed/<version> (+<base-url>)` User-Agent.
- **Conditional GET:** send `If-None-Match` / `If-Modified-Since`; surface 304 as `NotModified`
  (no body, no reparse) — the biggest polling win.
- **Per-host concurrency:** lazily-built `map[host]*semaphore` (default cap **3**) guarded by a
  mutex, with idle eviction so the map can't grow unbounded. Global ceiling = worker count.
- **Backpressure:** parse `Retry-After` on 429/503 and feed it into the reschedule backoff.
  (Token-bucket rate limiting from §10.2 is deferred.)

## 11. Sanitise (`sanitize`)

bluemonday allowlist: permit semantic content tags; strip `<script>`, `<style>`, `<iframe>`,
`<object>`, `<form>`, all `on*` handlers. Strip tracking query params from links; drop 1×1
pixel images. Resolve relative URLs against the entry/base URL. **Output is what gets stored.**
(Image-proxy `<img>` rewrite from §10.6 is deferred — MVP images load from origin.)

> **As built (iter 5):** the image proxy is now live. `GET /img?u=&s=` proxies images through
> a signed, same-origin endpoint (HMAC sig; bad sig → 403, never an open relay). All outbound
> fetches (polls, scrapes, and image proxying) share one SSRF-guarded `Fetcher`
> (private/loopback/link-local/metadata/CGNAT blocked at the dial layer;
> `BFEED_ALLOW_PRIVATE_CIDRS` escape hatch). `<img src>` is rewritten at **render time** in the
> web reader view — stored content keeps canonical origin URLs, so existing entries are proxied
> immediately, secret rotation is harmless, and toggling off (`BFEED_IMAGE_PROXY=off`) is clean.
> HMAC key from `BFEED_IMAGE_PROXY_SECRET` else generated once and persisted in the new
> `app_settings` table. Default ON. `srcset` rewriting and an image cache are deferred.

## 12. Web UI (`web`)

- Server-rendered `html/template` + htmx. Single-column, content-first, serif body text.
  Minimal embedded CSS; no build step, no bundler. Static assets via `embed.FS` with cache headers.
- **Views / routes:**

```
GET  /                      unread list (home), keyset "load more"
GET  /feeds                 all feeds (with last error surfaced)
GET  /feeds/{id}            entries for one feed
GET  /starred               starred entries (completes the star action; uses idx_entries_starred)
GET  /history               read entries ordered by read_at desc (uses idx_entries_readhist)
GET  /entries/{id}          single entry read view (sanitised HTML)
POST /feeds                 subscribe (URL form field; discovery if HTML)
POST /feeds/{id}/refresh    force refresh now            (htmx fragment)
POST /feeds/{id}/delete     delete feed (+entries, +tombstones)
POST /entries/{id}/read     mark read / unread (toggle)  (htmx fragment)
POST /entries/{id}/star     star / unstar (toggle)       (htmx fragment)
POST /entries/{id}/delete   delete entry (+tombstone)    (htmx fragment)
GET  /healthz               liveness (unauthenticated)
```

- Entry HTML rendered already-sanitised. No login (tailnet boundary) — no auth middleware, no
  CSRF for the MVP. (When auth lands, mutating routes gain CSRF + a session guard.)

## 13. Configuration (env, 12-factor subset)

```
BFEED_LISTEN_ADDR        :8080
BFEED_BASE_URL           external URL (User-Agent, absolute links)
BFEED_DATABASE_PATH      ./bfeed.db
BFEED_LOG_LEVEL          info
BFEED_LOG_FORMAT         json
BFEED_POLL_TICK          1m        # scheduler tick
BFEED_POLL_INTERVAL      15m       # fixed per-feed interval
BFEED_FEED_WORKERS       20        # worker pool size
BFEED_HOST_CONCURRENCY   3         # max concurrent requests per host
```

Validated at startup; sensible defaults. (Deferred features add their own vars later.)

## 14. CLI (`cmd/bfeed`, stdlib `flag`)

```
bfeed serve         run HTTP server + background poller (default if omitted)
bfeed migrate       apply schema migrations (goose); serve also auto-migrates on boot
bfeed healthcheck   probe local /healthz, exit 0/1 — for container HEALTHCHECK
bfeed version       version, git commit, build date
```

Deferred subcommands (`user`, `token`, `import`, `export`) arrive with their features.

## 15. Observability

**slog** everywhere: leveled, structured (JSON prod / text dev). High-cardinality detail to
logs: per-feed poll timing, per-feed errors with URL/status, request ids. **No Prometheus**,
no `/metrics` in the MVP — added wholesale per `design.md` §20 in a later iteration.

## 16. Lifecycle

`cmd/bfeed` builds a root `context.Context` cancelled on SIGINT/SIGTERM.
Startup: load+validate config → open SQLite (pragmas, single-writer pool) → run migrations
(goose) → seed user row if absent → construct adapters → construct services → start scheduler +
worker pool → start HTTP server. Shutdown: cancel ctx → HTTP `Shutdown` (drain) → scheduler
stops → pool finishes/aborts on ctx → `PRAGMA optimize` → close DB. `errgroup`/`WaitGroup` with
a drain timeout.

## 17. Testing strategy (TDD: Red/Green/Refactor)

- **Core services:** unit-tested with in-memory fakes implementing the ports + a **fake Clock**.
  Backoff/reschedule math tested deterministically.
- **`store/sqlite`:** integration-tested against real SQLite (`:memory:`/file) — sqlc queries,
  migrations, STRICT constraints, cascade deletes, tombstone skip-on-upsert, keyset pagination,
  `EXPLAIN QUERY PLAN` on the hot list query (expect index use, no temp B-tree).
- **`fetch`:** `httptest.Server` — conditional GET (304), Retry-After, per-host concurrency,
  timeouts.
- **`parse`/`sanitize`:** golden-file tests over fixture feeds/HTML, including malicious HTML
  asserting active content + trackers are stripped and relative URLs resolved.
- **`web`:** `httptest` handler tests — routing, scoping to `user_id=1`, htmx fragments.
- **Smoke:** wire real components, subscribe to a local test feed, assert entries appear, mark
  read/star/delete behave, poller reschedules.

## 18. MVP invariants (subset of `design.md` §27)

**Safety**
1. HTML is **always** sanitised before persistence; raw feed HTML never reaches the DB.
2. Sanitised HTML contains no `<script>`, event handlers, or active embeds.

**Politeness**
3. Feed fetches attempt conditional GET when validators are stored.
4. Per-host concurrency never exceeds the cap; global concurrency never exceeds the pool size.
5. `Retry-After`/429/503 is honoured; error reschedule uses exponential backoff with jitter.

**Data integrity**
6. A tombstoned `(feed_id, guid)` is never re-created by polling.
7. Deleting a feed removes its entries (and their per-feed tombstones cascade away). Single-entry
   deletes leave a tombstone that blocks re-poll resurrection while the feed exists.
8. `(feed_id, guid)` is unique; re-fetched entries upsert, never duplicate.

**Isolation (forward-compatible)**
9. Every data query is scoped by `user_id` (always `1` in the MVP); no id is trusted without it.

**Persistence**
10. All tables `STRICT`; timestamps INTEGER Unix-seconds UTC; booleans 0/1 with CHECK;
    `foreign_keys=ON`; single-writer pool; pagination is keyset, never OFFSET.

**Architecture**
11. `internal/core` imports no adapter; dependencies point inward; interfaces are consumer-owned.
12. Time-dependent logic uses the injected `Clock`, never `time.Now()` directly.

## 19. Suggested build order (iterate inside the MVP)

1. `store/sqlite` schema + migrations + sqlc queries (+ tests). Boring foundation first.
2. `parse` + `sanitize` adapters with golden tests.
3. `fetch` polite client with `httptest`.
4. `core` types/ports/services (`FeedService`, `EntryService`) against fakes.
5. `Poller` wiring services + adapters; smoke test end-to-end from CLI.
6. `web` UI last — once subscribe→poll→read works headless, put a face on it.

Each step is independently testable and leaves the tree green.

## 20. Path back to the full design

**Everything deferred is tracked exhaustively in [`roadmap.md`](./roadmap.md)** — the single
source of truth for what's left out, each item mapped to its `design.md` section and the
additive surface (table/column/package/route/env) it introduces, plus a suggested iteration
sequence. As features ship they move to the roadmap's **Done** section. Each is additive; none
invalidates MVP code or data.
