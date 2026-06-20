# Changelog

All notable changes to bfeed are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

How to maintain this file is documented in [docs/changelog.md](docs/changelog.md):
every code-change PR adds an entry under `[Unreleased]`; at release time that
section is renamed to the new version and becomes the GitHub Release notes.

## [Unreleased]

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

[Unreleased]: https://github.com/bcrisp4/bfeed/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bcrisp4/bfeed/releases/tag/v0.1.0
