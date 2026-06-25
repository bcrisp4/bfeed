# Changelog

All notable changes to bfeed are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

How to maintain this file is documented in [docs/changelog.md](docs/changelog.md):
every code-change PR adds an entry under `[Unreleased]`; at release time that
section is renamed to the new version and becomes the GitHub Release notes.

## [Unreleased]

### Added

- Feed title, URL, category, and full-content setting can now be edited inline on the Feeds page. Click the edit icon on any feed row to open the edit panel; save with one click or cancel to dismiss.

### Removed

- The separate per-field category and full-content toggle endpoints (`POST /feeds/{id}/category` and `POST /feeds/{id}/full-content`) are replaced by the unified inline edit form (`POST /feeds/{id}`).

- Feeds are now polled adaptively: active feeds are checked more often and quiet feeds less, within configurable bounds (`BFEED_SCHED_MIN_INTERVAL`, default 5m; `BFEED_SCHED_MAX_INTERVAL`, default 24h; `BFEED_SCHED_FACTOR`, default 1).
- bfeed now honours a feed's own update hints (RSS `<ttl>` and the syndication module) so it never polls faster than a publisher asks, capped so a malformed hint can't silence a feed.
- The Feeds page now shows a "⚠ stalled" badge on feeds that have failed repeatedly (after `BFEED_FEED_ERROR_LIMIT` consecutive errors, default 20), with the last error on hover.

### Changed

- **Breaking (config):** the single `BFEED_POLL_INTERVAL` is replaced by `BFEED_SCHED_MIN_INTERVAL` / `BFEED_SCHED_MAX_INTERVAL` / `BFEED_SCHED_FACTOR`. Update your environment: a feed previously polled every 15m now polls between 5m and 24h based on its activity.

## [0.6.0] - 2026-06-22

### Added
- Image proxy: entry images now load through a signed, same-origin `/img` endpoint, so your browser never contacts the origin or third-party tracker servers when viewing an article. Enabled by default; set `BFEED_IMAGE_PROXY=off` to load images directly instead.

### Security
- All outbound fetches (feed polls, article scrapes, and image proxying) now reject private, loopback, link-local, and cloud-metadata addresses (SSRF protection), on by default. Permit specific ranges with `BFEED_ALLOW_PRIVATE_CIDRS` (for example a feed hosted on your tailnet or LAN), or disable the guard with `BFEED_BLOCK_PRIVATE_NETWORKS=off`. While the guard is on it inspects the real destination, so `HTTP_PROXY`/`HTTPS_PROXY` are honoured only when the guard is disabled (a proxy would otherwise hide the destination from the check).

## [0.5.0] - 2026-06-21

### Added

- An unread count on the Unread view and on each feed's own page, plus unread and total counts beside every feed on the Feeds page.
- A clear message on empty lists, so an empty view no longer looks like a failed load.

### Changed

- Dates older than a day now read as "2 May 2026" (recent items still show "2h ago"), with the full date and time shown on hover.
- Tapping anywhere on an item in a list now opens it, not just its title, and list items highlight on hover.
- More breathing room around the icons in the mobile bottom bar.

### Fixed

- The star button in the reading view now updates immediately when tapped, instead of needing a page reload.
- The circled "read" tick is now sized to match the star and delete icons.

## [0.4.0] - 2026-06-21

### Added

- Mark all entries in a feed as read, in one click from the feed page.
- Star, mark-unread, and delete controls in the reading view.

### Changed

- Redesigned entry action buttons: clearer icons, a larger star, and bigger tap targets.
- Larger, clearer icons in the mobile navigation bar.
- Adding a feed now stays on the Feeds page and shows the new feed, instead of jumping to Unread.
- Pages, styles, and scripts are now sent compressed (gzip/brotli) and the main body font is preloaded, for faster page loads on slow or low-bandwidth connections.

### Fixed

- After an update, the app's styles and scripts now refresh immediately instead of being served from a stale browser cache (up to an hour) until a manual hard refresh.
- Entries opened from a list now reliably show as read after navigating back, and Mark read works on the first press.
- The search page no longer overflows its column, and its redundant instructions were removed.

## [0.3.1] - 2026-06-21

### Fixed

- Feeds that publish an empty title now show their feed URL as the name instead of a blank, unclickable entry on the Feeds page.

## [0.3.0] - 2026-06-21

### Added

- Redesigned web UI: Light/Sepia/Dark themes (defaults to your OS preference) with a Preferences page, self-hosted Literata + IBM Plex Mono typography, per-post summaries in lists, and reading-time on the reading view.
- Mobile-first navigation: a thumb-reachable bottom tab bar with a "More" sheet on phones.
- Opt-in per-feed full-content extraction — bfeed can fetch and extract the full
  article text (Readability) for feeds you flag, replacing the feed-provided
  snippet; falls back to feed content when extraction is disabled or fails.
  Configurable via `BFEED_SCRAPE_WORKERS`, `BFEED_SCRAPE_TICK`,
  `BFEED_SCRAPE_BATCH`, `BFEED_SCRAPE_MAX_ATTEMPTS`.

### Changed

- Article rendering now contains over-wide images, code blocks, and tables so they no longer break the page layout.
- The add-feed form now lives on the Feeds page; form inputs and dropdowns follow the active theme instead of staying bright; buttons and fields share a consistent height; and buttons, links, and controls give hover feedback.
- Search moved out of the top bar to its own nav item and page (it no longer overhangs the header divider).
- List previews now prefer real article text: a feed's summary when it reads as prose, otherwise the article's own opening (including scraped full content), and nothing when neither carries real text. Link/metadata-only summaries (e.g. a Hacker News item whose description is just "Article URL: … Comments URL: …") now show the article body instead of the raw links.

## [0.2.0] - 2026-06-20

### Added

- Feed categories — organise feeds into named categories; assign a category
  when subscribing and from the feeds page; filter the entry list by category.
- Full-text search (SQLite FTS5) — search box in the nav bar and a `/search`
  results view, BM25-ranked across entry title and content.

### Fixed

- Entry detail now renders the feed-provided summary when an entry has no
  content element (previously the body could render blank).

## [0.1.0] - 2026-06-19

First release. A self-hosted RSS/Atom/JSON feed reader as one pure-Go binary
(`CGO_ENABLED=0`) over one SQLite file with an htmx UI. Single-user MVP — no
auth; the tailnet is the security boundary.

### Added

- Subscribe to RSS, Atom, and JSON feeds by URL, with best-effort feed
  discovery from an HTML page (`<link rel="alternate">`) and an immediate poll
  on subscribe to populate title and entries.
- Background poller — fixed interval, one bounded worker pool, conditional GET
  (304 short-circuit, no reparse), per-host concurrency cap, and exponential
  backoff with jitter honouring `Retry-After` on 429/503.
- HTML sanitisation (bluemonday allowlist) before persistence — strips
  `<script>`/`<style>`/`<iframe>`/`<object>`/`<form>` and all `on*` handlers,
  drops 1×1 tracking pixels, strips tracking query params (`utm_*`, `fbclid`,
  …), and resolves relative URLs. Raw HTML never reaches the database.
- Entry storage — upsert by `(feed_id, guid)`, content-hash detection of
  in-place edits, and tombstones that prevent re-poll resurrection of deleted
  entries (feed delete cascades entries and tombstones).
- htmx web UI — mobile-first single column with unread (home), all feeds,
  single feed, starred, history, and single-entry read views; mark read/unread,
  star/unstar, delete entry, delete feed, and keyset "load more" as fragments.
- CLI subcommands `serve` (default), `migrate`, `healthcheck`, and `version`;
  auto-migrate on boot; graceful shutdown draining the HTTP server and poller.
- 12-factor environment config validated at startup, structured slog logging
  (JSON in prod, text in dev), and a distroless container image.

[Unreleased]: https://github.com/bcrisp4/bfeed/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/bcrisp4/bfeed/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/bcrisp4/bfeed/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/bcrisp4/bfeed/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/bcrisp4/bfeed/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/bcrisp4/bfeed/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/bcrisp4/bfeed/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/bcrisp4/bfeed/releases/tag/v0.1.0
