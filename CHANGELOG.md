# Changelog

All notable changes to bfeed are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

How to maintain this file is documented in [docs/changelog.md](docs/changelog.md):
every code-change PR adds an entry under `[Unreleased]`; at release time that
section is renamed to the new version and becomes the GitHub Release notes.

## [Unreleased]

### Added

- Reader view: wide images and figures now bleed past the text column — up to a comfortable width (about 1.6x the reading measure) on large screens, edge-to-edge on phones. Small images keep their natural size, captions stay at the reading measure, and article text never leaves its column.

- Optional Prometheus metrics: set `BFEED_METRICS_ADDR` to a bind address (e.g. `:9090`) to expose feed-poll, article-scrape, HTTP request, error, and backlog metrics at `/metrics` on that separate listener, alongside its own `/healthz`. Metrics stay off by default (no bind configured, no listener started).
- `GET /readyz` readiness probe alongside the existing `/healthz` liveness check, so orchestrators can distinguish "process is up" from "database is reachable".

### Changed

- Configuration now fails fast at startup when an environment variable is set to a value it can't parse (for example a duration written without a unit), naming the offending variable, instead of silently ignoring it and falling back to the built-in default.
- List and search pages are faster and lighter on large libraries: entry lists now read only a short preview of each article instead of loading whole full-text articles for every row, the unread and per-feed counts in list headers and self-refreshing feed rows are served by targeted queries instead of a full scan of every entry, and the image proxy streams images through to the browser instead of buffering each one whole in memory.
- Polling a feed now skips re-processing entries that haven't changed since the last poll, so feeds that don't support conditional requests are cheaper to keep up to date.

### Fixed

- The `migrate` and `healthcheck` subcommands no longer require `BFEED_BASE_URL` to be set, since neither uses it — running database migrations from an init container or probing container liveness no longer fails on unrelated configuration.
- Entry and feed titles and author names that contain HTML markup or encoded entities now display as clean text instead of showing literal tags or `&#39;`-style entity codes.
- Full-text search now matches the visible words of your articles instead of the underlying HTML markup, so searching for common terms like "https", "img", or "blank" no longer matches nearly every entry, and results are ranked more sensibly. Existing articles are re-indexed automatically on upgrade.
- List entry previews no longer occasionally show a stray fragment of an HTML tag or a broken character at the end of the blurb when the source content is truncated at an awkward spot.
- Full-text article extraction no longer permanently gives up on a feed's articles when the source is temporarily rate-limiting (HTTP 429) or briefly unavailable (5xx): those responses are now retried later, honouring the server's `Retry-After`, instead of counting against the give-up limit — so turning on full-text extraction for a whole feed at once can't burn out its backlog against a momentary rate limit.
- Re-enabling full-text extraction for a feed now gives previously failed articles a fresh set of retry attempts, instead of immediately re-failing them after a single new try.
- When full-text extraction ultimately fails for an article, the reader view now shows a brief note (including the reason) explaining it is showing the feed's own content, and the failure reason is recorded for troubleshooting.
- A feed subscribed by its site homepage whose initial feed auto-discovery failed (for example, a network blip or timeout during setup) now recovers automatically on a later poll or manual refresh, instead of staying stuck re-fetching the site page and failing forever.
- Changing a feed's address to one you already follow while also editing its other details now reports the conflict without partially applying the title/category changes, so the edit form's error message accurately reflects that nothing was saved.
- Enabling full-text extraction now switches the setting on and queues the existing backlog for extraction as a single atomic operation, so a transient database error can't leave extraction turned on with the existing articles never queued.
- A feed subscription that fails during its initial setup — including when it times out — now reliably shows the error on the feed, instead of appearing to have no status until a later scheduled poll happens to record one.
- Feed rows refreshed in place (during a background refresh or inline edit) no longer briefly flash a misleading "0 unread / 0" when the entry-count lookup momentarily fails; the counts are omitted instead, matching the Feeds page.
- Deleting an entry, or marking one unread, that fails to save is now reported as an error instead of looking like it succeeded — previously the row vanished (or you were redirected) as if it worked, and the entry reappeared later with no explanation.
- A momentary database error while opening an entry, category, or feed now returns a proper server error instead of a misleading "not found".
- Editing a feed's details with an invalid or already-subscribed address now shows the error inline in the edit form instead of the Save button silently doing nothing.
- "Load more" on a long entry list no longer leaves a stale button mid-list or re-appends the same page as duplicate entries; the button now advances cleanly to the next page.
- Opening a link to a feed that has since been deleted now returns a proper "not found" page instead of an empty untitled "Feed" page, and a single feed's page now shows the feed's name in its heading.
- Deleting a feed or a category now immediately refreshes the affected feed/unread counts on the page instead of leaving stale totals until a manual reload; deleting an already-deleted category no longer reports a server error.
- Adding or renaming a category to a name that already exists (or a blank name) now shows a friendly inline message on the Categories page instead of dumping you on a raw error page.
- The unread count in a list's header now updates immediately when you mark or delete an entry from that list, instead of showing the old number until you navigate away.
- When the entry-count lookup fails, the Feeds page now omits the per-feed counts rather than showing a misleading "0 unread / 0".
- Deleting a feed and immediately subscribing to another no longer risks the new feed silently inheriting the deleted one's address, title, and articles (or the new feed's own setup being skipped). Feed identifiers are no longer reused after deletion.
- Editing a feed's address while it is refreshing (including the delete-and-re-add repair flow) no longer leaves it stuck showing the old address's content and update markers, and the new address is now fetched promptly instead of after a long delay.
- Turning on full-text article extraction for a feed while it is mid-refresh now also extracts the entries that arrive during that refresh, instead of leaving them permanently un-extracted until you toggle the setting off and on again.
- A just-added feed no longer briefly flips to an error state when a scheduled poll races its initial setup.
- Opening a single feed's entries or the Starred page is now fast on large libraries: both are served directly from their keyset indexes instead of scanning your entire entry history on every page load.
- Feeds that permanently move to a new address (a `301`/`308` redirect) are now followed to the new URL and remembered, so bfeed stops re-fetching the old redirecting address on every poll and a later re-subscribe to the new address no longer creates a duplicate feed. Temporary redirects are left as-is.
- Images and links now resolve correctly when a fetch redirects (for example, via a link-tracking hop or a moved site): relative URLs in full-text (reader-view) articles and in feed entry content are resolved against the address the content was actually served from instead of the original link, so they no longer break. Feed auto-discovery resolves relative feed links the same way.
- Feeds served in a non-UTF-8 character set (declared only via the HTTP `Content-Type` header, as many older feeds are) now ingest correctly instead of failing every poll and getting permanently stuck; their text is decoded to the right characters. Feeds that declare their encoding in the document itself continue to work unchanged.
- Entries published without a date are now ordered by when bfeed first saw them, so they appear at the top of the list with everything else instead of being buried at the very bottom (and no longer show a nonsensical year-0001 timestamp).
- A newly created feed that has no entries yet (and no title) can now be subscribed to, instead of being rejected with "no feed found at URL" until it publishes its first item.
- A feed whose entries momentarily fail to save (for example, a full disk or a busy database) now backs off and retries instead of hammering the source at the polling tick rate; the same fix applies to full-content article extraction.
- A malformed or malicious feed or article that trips a bug in the parser, content extractor, or sanitiser can no longer crash the whole server: the failure is now contained to that one feed/entry (recorded as an error and retried with backoff) instead of taking the process down and getting stuck in a restart loop.
- `bfeed serve` now exits with a non-zero status when it cannot start the web server (for example, the listen address is already in use), so process supervisors and container restart policies react instead of treating a server that never started as healthy.
- Shutdown now waits for in-flight "add feed" and "refresh" operations to finish before closing the database, so a subscribe or refresh triggered just before shutdown is no longer silently lost.
- The container image now starts correctly with its default database path: the `/data` volume is created owned by the non-root user, so `docker run` (or a fresh named/anonymous volume) no longer fails on Linux with "unable to open database file". Previously the published image exited immediately unless you pre-created and chowned the volume by hand.
- Subscribing by a site's homepage address when you already follow that site's feed no longer leaves a broken duplicate feed that repeatedly fetches the wrong page; the redundant subscription is now dropped automatically.
- Feed addresses that differ only trivially (letter case, a default `:80`/`:443` port, or a trailing `#fragment`) are now treated as the same feed, so you can't accidentally subscribe to the same feed twice and see every article duplicated.
- Categories are now case-insensitive: creating or renaming a category to `news` when `News` already exists is rejected instead of producing two look-alike categories side by side.
- Renaming a feed now re-sorts it under its new name on the Feeds page and sidebar, instead of staying filed under its old publisher name.
- Feeds advertised only as a JSON Feed (the standard `application/feed+json` type) are now found by address auto-discovery instead of failing with "no feed found at URL".
- Feed items that arrive without a unique identifier no longer risk collapsing into a single entry (losing the others) when several share the same link and title; distinct items are now kept apart.

### Security

- bfeed now rejects requests whose `Host` header doesn't match its configured address (the `/healthz` endpoint is exempt so container health checks keep working), blocking DNS-rebinding attacks that could let a malicious website you visit read your feeds and drive bfeed from your browser.
- App pages now send a strict `Content-Security-Policy` (plus `X-Frame-Options`, `Referrer-Policy: no-referrer`, and `X-Content-Type-Options: nosniff`), so a flaw in HTML sanitising can no longer be escalated to running scripts on the bfeed origin, and the page can't be framed by another site.
- Links inside feed articles now carry `rel="noreferrer"`, so clicking one no longer leaks your private bfeed address to the destination site.
- A malicious or misconfigured feed server can no longer park a feed arbitrarily far in the future via an oversized `Retry-After`; the delay is now capped at bfeed's maximum backoff.
- Oversized responses are now rejected outright instead of being silently truncated and treated as complete, so a cut-off article or a partially downloaded image is never persisted or served as if whole.
- The feed fetcher's private/loopback address blocklist (SSRF guard) now also covers several additional non-public IP ranges, and a stalled server can no longer hang a fetch forever now that a default request timeout always applies.

## [0.7.0] - 2026-06-25

### Added

- Rename a feed, or edit its URL, category, and full-content setting inline on the Feeds page. Click the edit icon on any feed row to open the edit panel; save with one click or cancel to dismiss.
- Feeds are now polled adaptively: active feeds are checked more often and quiet feeds less, within configurable bounds (`BFEED_SCHED_MIN_INTERVAL`, default 5m; `BFEED_SCHED_MAX_INTERVAL`, default 24h; `BFEED_SCHED_FACTOR`, default 1).
- bfeed now honours a feed's own update hints (RSS `<ttl>` and the syndication module) so it never polls faster than a publisher asks, capped so a malformed hint can't silence a feed.
- The Feeds page now shows a "⚠ stalled" badge on feeds that have failed repeatedly (after `BFEED_FEED_ERROR_LIMIT` consecutive errors, default 20), with the last error on hover.

### Changed

- Adding and refreshing feeds now run in the background with immediate feedback; per-feed counts update automatically without a page reload.
- The Feeds page is redesigned: feeds are grouped by category with per-feed unread and total counts, last-updated and next-update times, an icon action bar, and clearer error and pending states.
- **Breaking (config):** the single `BFEED_POLL_INTERVAL` is replaced by `BFEED_SCHED_MIN_INTERVAL` / `BFEED_SCHED_MAX_INTERVAL` / `BFEED_SCHED_FACTOR`. Update your environment: a feed previously polled every 15m now polls between 5m and 24h based on its activity.

### Removed

- The separate per-field category and full-content toggle endpoints (`POST /feeds/{id}/category` and `POST /feeds/{id}/full-content`) are replaced by the unified inline edit form (`POST /feeds/{id}`).

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

[Unreleased]: https://github.com/bcrisp4/bfeed/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/bcrisp4/bfeed/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/bcrisp4/bfeed/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/bcrisp4/bfeed/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/bcrisp4/bfeed/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/bcrisp4/bfeed/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/bcrisp4/bfeed/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/bcrisp4/bfeed/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/bcrisp4/bfeed/releases/tag/v0.1.0
