# bfeed — Roadmap & Deferred Backlog

> Status: living tracker. The **single source of truth for everything deliberately left out of
> the MVP**, so no capability is lost between iterations.
> - Full spec (north star): [`design.md`](./design.md)
> - MVP scope (iteration 1): [`mvp-design.md`](./mvp-design.md)
> - This file: every capability **not** in the MVP, mapped back to `design.md`, with what it
>   adds. Every item is **additive** — a new table/column/package/route/env var — so adding it
>   never rewrites MVP code or data.

As an item ships, move it from a backlog table to **§ Done** with the iteration number.

---

## Legend

- **Ref** — section in `design.md` holding the full specification.
- **Adds** — the concrete new surface (schema / package / route / env / CLI) the feature introduces.
- **Status** — `deferred` (not started). Update to `done (iter N)` when shipped.

---

## A. Backlog by area

### A1. Authentication, sessions & users
Tailnet is the MVP boundary; all of this is deferred until bfeed might be exposed or go multi-user.

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Web session login (username+password) | §16 | `sessions` table + `idx_sessions_user`; `users.password_hash`; argon2id; session cookie; `SessionStore`; `UserService` | First auth increment; gates the rest | deferred |
| CSRF protection on mutating routes | §16 | synchroniser token cookie + form/header; htmx config | Needs sessions first | deferred |
| Multi-user (per-user data already scoped by `user_id`) | §16, §11 | drop the hardcoded `user_id=1`; user-select on auth | Schema already carries `user_id` everywhere | deferred |
| Admin role + user management page | §16, §18 | `users.is_admin`; `/admin`; admin-guard middleware; `UserStore` CRUD | Needs multi-user | deferred |
| Bootstrap admin from env (first run) | §16 | `BFEED_ADMIN_USERNAME` / `BFEED_ADMIN_PASSWORD` | | deferred |
| CLI user management | §22 | `bfeed user <create|list|set-password|delete>` | | deferred |

### A2. Polling engine — adaptive & extra politeness
MVP uses a **fixed interval** + conditional GET + per-host concurrency cap + exp backoff.

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Adaptive refresh interval (poll active feeds more) | §12 | `schedule.go` pure fn; replaces fixed-interval reschedule | Core differentiator vs MVP | done (iter 6) |
| Weekly entry count (feeds the adaptive math) | §12 | `FeedStore.WeeklyEntryCount` SQL | Shipped as **`COUNT`-in-window** with ingest-time fallback, **not** the spacing-based virtual count originally specced (spacing min-pinned on tiny denominators — see the iter-6 review) | done (iter 6) |
| Per-feed `<ttl>`/`sy:*` honouring | §12 | `feeds.ttl_seconds` column; RSS `<ttl>` + syndication module in parser; capped (~30d) | Folded into iter 6 (was the "feedTTL term") | done (iter 6) |
| Per-host token-bucket rate limiter | §10.2 | `x/time/rate` per-host limiter alongside the semaphore | Politer; MVP has concurrency cap only | deferred |
| Tighten limiter on 429/503 (`SetLimit` until deadline) | §10.2 | limiter state per host | Needs token-bucket limiter | deferred |
| Honour robots `Crawl-Delay` | §10.2 | robots fetch/parse → `rate.Every(delay)` | | deferred |
| Hard error-limit dispatch exclusion | §12 | ~~`WHERE error_count < limit` in due query~~ | **Dropped (iter 6):** backoff already caps a dead feed at ~1 GET/day and a hard exclude risks permanent undispatch on a transient outage. `BFEED_FEED_ERROR_LIMIT` instead drives a Feeds-page "stalled" badge (done) | won't do |
| Per-feed interval override (min/max pin) | §28 | `feeds` columns for override | Open question in design | deferred |

### A3. Full-content extraction (scrape)
MVP shows feed-provided content only.

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Readability article scrape | §10.4, §13 | `extract/` package (`readeck/go-readability` v2); `Extractor` port | Sanitise output before store (invariant) | done (iter 4) |
| Decoupled scrape worker pool + queue | §13 | DB-backed `extract_state` + background `Scraper` sweep; `BFEED_SCRAPE_WORKERS` | Must not starve feed polling | done (iter 4) |
| Per-feed `FetchFullContent` opt-in | §7, §11 | `feeds.fetch_full_content` column; edit UI | | done (iter 4) |
| `SetEntryContent` post-extraction write | §8 | `EntryStore.SetEntryContent` | | done (iter 4) |
| Backfill cap per host per cycle | §13 | `BFEED_BACKFILL_PER_HOST_PER_CYCLE`; live-over-backfill priority | Fixes "big new feed blocks for ages" — deferred; global per-cycle batch cap (`BFEED_SCRAPE_BATCH`) + shared per-host semaphore cover it for now | deferred |
| Dispatch-time `next_extract_at` lease | §13 | `Scraper.dispatch` bumps `next_extract_at` (e.g. `now+Tick`) before queueing an entry; likely a new `LeasePendingExtraction` store method | Prevents the `Scraper` re-dispatching still-in-flight entries when a batch's scrapes outlast a tick (duplicate fetch; a stale worker can revert a `done` entry). Same accepted characteristic the `Poller` has today | deferred |
| Re-scrape on in-place content change | §13 | reset `extract_state` → `pending` in `UpsertEntries`' hash-changed update branch when the feed has `fetch_full_content` | Today a hash-changed re-poll overwrites scraped full content with the feed snippet and leaves `done`, so edited articles are never re-scraped (documented "kept simple" limitation) | deferred |
| Shared backoff/scheduler abstraction | §12, §13 | factor the duplicated exponential-backoff math (`ExtractBackoff` vs `PollReschedule`) and worker-pool driver (`Scraper` vs `Poller`) onto a common helper | Reduce duplication — a fix to the backoff or drain logic must currently be applied in two places and will drift | deferred |

### A4. Image proxy / privacy hardening
MVP strips trackers/pixels but **images load from origin** (leaks reader IP). Acceptable on the tailnet.

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Signed image proxy endpoint | §10.6 | `imgproxy/` package; `GET /img?u=&s=`; HMAC signing | Never an open relay (signed URLs only) | done (iter 5) |
| SSRF guard (reject private/loopback/link-local/metadata IPs) | §10.6 | IP resolution + allowlist; `image/*` content-type allowlist | | done (iter 5) |
| Image cache (on-disk/LRU, size cap + TTL) | §10.6 | cache dir; `BFEED_IMAGE_CACHE_*` | | deferred |
| Proxy HMAC key resolution | §10.6 | `app_settings` table; `BFEED_IMAGE_PROXY_SECRET` else generate+persist | | done (iter 5) |
| Reader-view `<img src>` rewrite → proxy (render-time, not sanitiser) | §10.5 | web reader handler rewrites img URLs at render time; stored content keeps canonical origin URLs | Shipped as render-time rewrite, not sanitiser change | done (iter 5) |

### A5. Full-text search

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| FTS5 search over entries | §15 | `entries_fts` virtual table (external-content) + 3 sync triggers; `SearchIndex` port; `SearchService` | | done (iter 3) |
| Search UI + route | §18 | `/search?q=` view | Needs FTS | done (iter 3) |
| Porter stemming option | §28 | `tokenize='porter'` config flag | Default stays `unicode61` | deferred |

### A6. Organisation — categories

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Categorise feeds; filter by category | §9.1, §18 | `categories` table + `idx_categories_user`; `feeds.category_id`; `CategoryStore`; sidebar grouping; filter | | done (iter 3) |
| Category CRUD | §9.1 | category routes; delete → feeds become uncategorised | | done (iter 3) |

### A7. Reading features

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| History view (recently-read, by `read_at`) | §9.2, §18 | `idx_entries_readhist`; `/history` route | Cheap | done (iter 2) |
| Bulk mark-all-read | §9.2 | bulk UPDATE + button | Backend (`MarkReadByFilter`, filter-scoped: feed/category/all) shipped iter 4; UI button wired on the feed page only — global/category mark-all button still deferred | done (iter 4, backend) |
| Feed enable/disable via UI | §9.1 | toggle route (schema `feeds.disabled` already present; poller already respects it) | UI only; backend ready | deferred |
| Rename feed / custom title | §9.1 | `feeds.user_title` override column; display = `user_title ?? title` via `Feed.DisplayTitle()` | Must be a **separate** override column (poll clobbers `feeds.title`). Shipped folded into a unified `FeedService.EditFeed` + inline-edit panel (`GET /feeds/{id}/edit`, `POST /feeds/{id}`), **not** a standalone `/rename` route — the same panel also edits URL/category/full-content. Background subscribe/refresh + self-polling row landed alongside. | done (iter 7) |
| Starred view | §18 | `/starred` route | **Shipped in MVP** (star action would be a half-feature without it) — see mvp-design §12 | done (MVP) |

### A8. Retention & maintenance
MVP writes a **tombstone on single-entry delete** (to block re-poll resurrection while the feed
exists); deleting a whole feed cascades its entries *and* tombstones away and writes none. There
is **no cleaner and no tombstone pruning** — single-delete tombstones and read entries accumulate.
Fine short-term; revisit before the DB grows large.

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| TTL cleaner (delete read+unstarred older than TTL) | §14 | `cleaner.go`; `EntryStore.DeleteExpired`; `idx_entries_ttl`; `BFEED_CLEANUP_INTERVAL` | Never touches unread/starred | deferred |
| Per-user retention TTL | §14 | `users.entry_ttl_days`; `BFEED_DEFAULT_ENTRY_TTL_DAYS` (365) | | deferred |
| Tombstone pruning (bound growth) | §14 | `Maintenance.PruneTombstones`; `idx_tombstones_age` | Prevents unbounded tombstone table | deferred |
| WAL checkpoint/optimize after large deletes | §10.1, §14 | `Maintenance.Optimize`; `wal_checkpoint(TRUNCATE)` | | deferred |
| DB stats surface | §8 | `Maintenance.DatabaseStats` | | deferred |

### A9. REST API

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Full REST API under `/v1` (JSON) | §17 | `api/` package; all `/v1/*` routes | | deferred |
| Bearer-token auth + scoping | §16, §17 | `api_tokens` table + `idx_api_tokens_user`; `TokenStore`; SHA-256 lookup; read-only scope | | deferred |
| Token management (UI + CLI) | §16, §22 | `/v1/tokens`; `bfeed token <create|list|revoke>` | Plaintext shown once | deferred |
| Error envelope + sentinel→status mapping | §17 | `{error:{code,message}}`; 404/409/401/422 | | deferred |

### A10. Data portability — OPML

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| OPML import | §19 | parse OPML → loop subscribe (dedupe, best-effort); `bfeed import` | Fast way to populate | deferred |
| OPML export | §19 | serialise feeds+categories; `bfeed export`; `/v1/export` | Anti-lock-in | deferred |

### A11. Mobile / UI niceties

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Add-to-home-screen (PWA, no service worker) | §2, §18 | `manifest.webmanifest`, app icons, `apple-touch-icon` + meta | Stated core want | deferred |
| Localised / local-timezone time display | §18 | render times in the viewer's timezone + locale-aware date format — a small client-side formatter over the existing RFC3339 `<time datetime>`, or a timezone pref (cookie for single-user, per-user setting under A1) | Today times render as the stored **UTC** instant in a fixed `2 Jan 2006` / `2 Jan 2006, 15:04` format (`humanizeSince` + `toEntryVM`); fine for a single-user self-host on one timezone, but a viewer in another zone sees UTC. The `<time>` already carries a machine-readable RFC3339 `datetime`, so client-side rendering is the cheap additive path | deferred |
| Light/dark/system theme toggle | §2, §18 | CSS vars + `prefers-color-scheme` + cookie | Light/Sepia/Dark, default follows OS — specced in `specs/2026-06-20-web-ui-redesign-design.md` | done (iter 3) |
| Settings page (theme, summary, width) | §18 | `/settings` (cookie-backed, single-user) | Cookie-backed prefs specced in the UI-redesign spec; per-user TTL still needs auth+retention | done (iter 3) |
| Persist user prefs in DB (multi-user) | §16, §18 | move the cookie-backed prefs (`bfeed_theme`/`bfeed_summary`/`bfeed_width`) onto a per-user `user_settings` table (or `users` columns) | When multi-user/auth (A1) lands — cookies are the deliberate single-user MVP form, so this is the additive upgrade path | deferred |
| Content-hashed (fingerprinted) static asset URLs | §18 | `assetURL` hashes each embedded asset at startup; templates reference CSS/JS via the `asset` func as `…?v=<hash>`; `cacheStatic` serves any `?v=` request (and woff2) `immutable` | A `?v=` URL auto-busts on change and is cached for a year; a bare un-fingerprinted hit still gets `max-age=3600` as a safety net. Built-in on-the-fly gzip/brotli compression (`CAFxX/httpcompression`, allowlisted text types only — woff2 skipped) also landed alongside | done |

### A12. Observability — Prometheus
MVP is **slog only**.

| Capability | Ref | Adds | Notes / deps | Status |
|---|---|---|---|---|
| Prometheus registry + `/metrics` | §20 | `prometheus/client_golang`; metrics bind | | deferred |
| Feed/scrape/HTTP histograms + queue gauges | §20 | `bfeed_feed_poll_duration_seconds`, `bfeed_*_queue_depth`/`_inflight`, HTTP metrics | | deferred |
| Error/attempt counters | §20 | `bfeed_errors_total{component,reason}`, `bfeed_feed_polls_total{result}` | Closed-enum labels only | deferred |
| Entity gauges | §20 | `bfeed_users_total`, `bfeed_feeds_total{user}`, `bfeed_entries_total` | | deferred |
| `go_sql_*` DB pool collector | §20 | `collectors.NewDBStatsCollector` | | deferred |
| `BFEED_METRICS_ADDR` separate bind | §21 | config | | deferred |

---

## B. Consolidated additive surface (cross-cutting checklists)

So a future iteration can see at a glance what it adds.

### B1. Schema additions (additive migrations)
- Tables: `categories`, `api_tokens`, `sessions`, `app_settings`, `entries_fts` (+ 3 triggers).
- Columns: `users.password_hash`, `users.is_admin`, `users.entry_ttl_days`;
  `feeds.category_id`, `feeds.fetch_full_content`; per-feed interval-override columns.
- Indexes: `idx_categories_user`, `idx_api_tokens_user`, `idx_sessions_user`,
  `idx_tombstones_age`, `idx_entries_ttl`.
  (`idx_entries_readhist` shipped in iter 2 with the history view.)

### B2. Config (env) additions
- Scheduling: `BFEED_BATCH_SIZE`, `BFEED_SCHED_MIN_INTERVAL`, `BFEED_SCHED_MAX_INTERVAL`,
  `BFEED_SCHED_FACTOR`, `BFEED_FEED_ERROR_LIMIT`.
- Politeness: `BFEED_HOST_RATE_PER_SEC`, `BFEED_HOST_BURST`.
- Scrape: `BFEED_SCRAPE_WORKERS`, `BFEED_BACKFILL_PER_HOST_PER_CYCLE`.
- Retention: `BFEED_DEFAULT_ENTRY_TTL_DAYS`, `BFEED_CLEANUP_INTERVAL`.
- Auth: `BFEED_SESSION_TTL`, `BFEED_ADMIN_USERNAME`, `BFEED_ADMIN_PASSWORD`.
- Privacy: `BFEED_IMAGE_PROXY`, `BFEED_IMAGE_PROXY_SECRET`, `BFEED_IMAGE_CACHE_DIR`,
  `BFEED_IMAGE_CACHE_MAX_BYTES`, `BFEED_IMAGE_MAX_BYTES`.
- Metrics: `BFEED_METRICS_ADDR`.

### B3. Package additions
`api/`, `extract/`, `imgproxy/`, plus core `search.go`, `cleaner.go`, `user.go`,
`schedule.go`, and the auth/session/token/category/maintenance store sub-interfaces.

### B4. CLI additions
`bfeed user …`, `bfeed token …`, `bfeed import …`, `bfeed export …`.

### B5. Open questions (decide before/at relevant iteration)
- **License** — ✓ resolved: **Apache-2.0** (`LICENSE` in repo root).
- **Read/write connection split** — only if single-writer + `busy_timeout` proves insufficient (§28).
- **Search stemming** — flip `unicode61`→`porter` if search feels too literal (§28).
- **Per-feed interval override** — add if a feed needs special cadence (§28).

---

## C. Suggested iteration sequence

Order chosen to unblock the most daily-driver value first; each iteration is additive and ships green.

| Iter | Theme | Items |
|---|---|---|
| 1 (MVP) | Core loop | see `mvp-design.md` |
| 2 | Reading polish | History view, bulk mark-all-read, feed enable/disable UI, theme toggle, PWA add-to-home |
| 3 | Find things | Categories ✓ done (iter 3); FTS5 search + UI ✓ done (iter 3) |
| 4 | Content quality | Full-content scrape (extract + scrape pool + backfill cap) ✓ done (iter 4) |
| 5 | Privacy | Image proxy: signed `/img` endpoint, render-time rewrite, SSRF-guarded fetch, `app_settings`-backed HMAC secret ✓ done (iter 5); image cache deferred |
| 6 | Smarter polling | Adaptive interval + weekly count; token-bucket limiter; error-limit; robots Crawl-Delay |
| 7 | Housekeeping | TTL cleaner + per-user TTL + tombstone pruning + WAL maintenance |
| 8 | Integrations | REST API + bearer tokens; OPML import/export |
| 9 | Multi-user | Auth (sessions/CSRF/argon2id) → multi-user → admin → settings page |
| 10 | Operability | Prometheus metrics + `/metrics` |

Sequence is a guide, not a contract — reorder by what hurts most in daily use.

---

## Done

_(Move shipped items here with their iteration number.)_
- Starred view (`/starred`) — MVP.
- History view (`/history`, read entries by `read_at`) — iter 2.
- Categories (feeds → categories, aggregated category stream, CRUD) — iter 3.
- Full-text search (FTS5 over title/content/summary, bm25-ranked, /search) — iter 3.
- Full-content extraction (opt-in per feed, DB-backed scrape sweep) — iter 4.
- Content-hashed (fingerprinted) static asset URLs (`?v=<hash>` → `immutable`) + on-the-fly gzip/brotli response compression + body-font preload — A11.
- Image proxy (signed same-origin /img, render-time rewrite, SSRF-guarded fetch with CIDR allowlist, app_settings-backed HMAC secret; cache deferred) — iter 5.
- Light/Sepia/Dark theme toggle (CSS vars + `prefers-color-scheme`, OS default, cookie) — iter 3.
- Settings/Preferences page (cookie-backed theme/summary/width, single-user) — iter 3.
- Bulk mark-all-read backend (`MarkReadByFilter`, feed/category/all scoped) + feed-page button — iter 4. Global/category mark-all UI button still deferred.
- License chosen: Apache-2.0 (`LICENSE`).
- Adaptive feed-poll scheduling (`COUNT`-in-window weekly count + ingest-time fallback, cold-start at min, capped publisher `<ttl>`/`sy:*`, success-interval jitter; `BFEED_SCHED_*` replace `BFEED_POLL_INTERVAL`) + Feeds-page "stalled" badge (`BFEED_FEED_ERROR_LIMIT`) — iter 6. Hard error-limit dispatch exclusion deliberately dropped; token-bucket limiter, robots Crawl-Delay, per-feed override still deferred.
