# bfeed — Roadmap

> **Future work is tracked in [GitHub issues](https://github.com/bcrisp4/bfeed/issues) and
> [milestones](https://github.com/bcrisp4/bfeed/milestones)** — the single source of truth for
> what remains. This file keeps only the tracking conventions, the shipped history, and what
> was deliberately dropped. (It used to be the full deferred-backlog tracker; that content
> moved to issues #54–#78 in July 2026.)
>
> - Full spec (north star): [`design.md`](./design.md)
> - MVP scope (iteration 1): [`mvp-design.md`](./mvp-design.md)

## How remaining work is organised

**Milestones** group the work by theme (one issue ≈ one Claude Code session):

| Milestone | Theme |
|---|---|
| [Reading & UI polish](https://github.com/bcrisp4/bfeed/milestone/2) | Feed enable/disable UI, mark-all-read everywhere, PWA add-to-home, local-timezone timestamps |
| [Scrape & politeness](https://github.com/bcrisp4/bfeed/milestone/3) | Extraction lease/re-scrape/backfill-cap, per-host token-bucket limiter, robots Crawl-Delay |
| [Storage](https://github.com/bcrisp4/bfeed/milestone/4) | TTL cleaner, tombstone pruning, WAL maintenance, DB stats |
| [Operability](https://github.com/bcrisp4/bfeed/milestone/5) | Prometheus metrics + `/metrics` |
| [Integrations](https://github.com/bcrisp4/bfeed/milestone/6) | OPML import/export, REST API + bearer tokens |
| [Multi-user](https://github.com/bcrisp4/bfeed/milestone/7) | Sessions/argon2id → CSRF → multi-user → admin → per-user prefs |

**Labels:**

- `size/S` / `size/M` — comfortable vs. full single-session scope.
- `blocked` — has an unmet dependency (named in the issue body); remove when the dependency closes.
- `refactor` — internal change, no user-facing behaviour (PR carries `skip-changelog`).
- `icebox` — deferred "revisit when it hurts" ideas, no milestone: porter stemming (#75),
  per-feed interval override (#76), poll-time image prefetch (#77), read/write connection
  split (#78).

Feature work stays **additive** — a new table/column/package/route/env var — so shipping one
never rewrites existing data or invalidates existing behaviour. `refactor` issues are the
exception: they restructure code, but must be behaviour- and data-compatible.

## Dropped (deliberate)

- **Hard error-limit dispatch exclusion** — backoff already caps a dead feed at ~1 GET/day and
  a hard exclude risks permanent undispatch on a transient outage. `BFEED_FEED_ERROR_LIMIT`
  instead drives the Feeds-page "stalled" badge (shipped).
- **On-demand server-side image cache** — browser caching already delivers the perf
  (`/img` serves year-long `immutable` responses with deterministic signed URLs), and an
  on-demand cache cannot fix the remaining privacy leak (read-timing). Re-scoped as poll-time
  image prefetch in icebox issue #77.

## Done

- Starred view (`/starred`) — MVP.
- History view (`/history`, read entries by `read_at`) — iter 2.
- Categories (feeds → categories, aggregated category stream, CRUD) — iter 3.
- Full-text search (FTS5 over title/content/summary, bm25-ranked, /search) — iter 3.
  Restructured by audit B12 (migration `0013`): FTS now indexes plain-text projections.
- Light/Sepia/Dark theme toggle (CSS vars + `prefers-color-scheme`, OS default, cookie) — iter 3.
- Settings/Preferences page (cookie-backed theme/summary/width, single-user) — iter 3.
- Full-content extraction (opt-in per feed, DB-backed scrape sweep) — iter 4.
- Bulk mark-all-read backend (`MarkReadByFilter`, feed/category/all scoped) + feed-page button — iter 4.
- Image proxy (signed same-origin /img, render-time rewrite, SSRF-guarded fetch with CIDR allowlist, app_settings-backed HMAC secret) — iter 5.
- Adaptive feed-poll scheduling (`COUNT`-in-window weekly count + ingest-time fallback, cold-start at min, capped publisher `<ttl>`/`sy:*`, success-interval jitter; `BFEED_SCHED_*` replace `BFEED_POLL_INTERVAL`) + Feeds-page "stalled" badge (`BFEED_FEED_ERROR_LIMIT`) — iter 6.
- Rename feed / custom title (`feeds.user_title` + `Feed.DisplayTitle()`), unified inline feed edit (`EditFeed`: title/URL/category/full-content), background subscribe/refresh with self-polling feed rows — iter 7.
- Content-hashed (fingerprinted) static asset URLs (`?v=<hash>` → `immutable`) + on-the-fly gzip/brotli response compression + body-font preload.
- 2026-07 audit remediation, batches B1–B13 (84 findings: correctness, security hardening, performance/indexing, htmx, error handling) — see [`audit-2026-07.md`](./audit-2026-07.md).
- License chosen: Apache-2.0 (`LICENSE`).
