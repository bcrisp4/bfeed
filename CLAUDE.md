# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

bfeed — a self-hosted RSS/Atom/JSON feed reader: one pure-Go binary (`CGO_ENABLED=0`, no cgo), one SQLite file, htmx UI. Single-user MVP (no auth; tailnet is the boundary). Module `github.com/bcrisp4/bfeed`, Go 1.25+.

The design is documented and authoritative — read it before non-trivial work:
- `docs/design.md` — full north-star spec (the long-term target).
- `docs/mvp-design.md` — the scope that is **actually built** right now (iteration 1). When code and `design.md` disagree, this is why.
- `docs/roadmap.md` — everything deliberately deferred, with the additive path back.
- `docs/releasing.md` — how to cut a release (annotated semver tag → goreleaser).
- `docs/changelog.md` — the changelog policy (what to write, when, how CI enforces it).

(Implementation plans under `docs/superpowers/plans/` are gitignored, per the user's "don't commit plans" rule. `bfeed.db` and WAL files in the repo root are gitignored local dev state.)

## Changelog (mandatory)

`CHANGELOG.md` ([Keep a Changelog](https://keepachangelog.com/en/1.1.0/)) is the
single source of truth for release notes. **Every PR that changes behaviour must
add an entry under `[Unreleased]`** — CI's `changelog` job fails the PR otherwise.
Write entries from the user's perspective under the right category (`Added`,
`Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`). PRs with no user-facing
change (CI/tooling, pure refactors, test-only, deps, docs) carry the
`skip-changelog` label instead. Full policy and the release-time
`[Unreleased]` → version roll: `docs/changelog.md`.

## Commands

```bash
make build           # CGO_ENABLED=0 build of ./cmd/bfeed (must stay cgo-free)
make test            # all tests           (or: go test ./...)
make test-race       # race detector — run before declaring done
go test ./internal/core/ -run TestName -v    # a single test
make lint            # golangci-lint v2 (gofumpt+goimports, vet, staticcheck, gosec, govulncheck-equivalent)
make fmt             # apply gofumpt/goimports
make sqlc-check      # fail if committed sqlc code is stale (CI-enforced)
make run             # serve locally (sets the required BFEED_BASE_URL)
make image           # build the container image locally with docker
```
golangci-lint v2 (config in `.golangci.yml`) is the lint bar — it runs `go vet`,
formatting (gofumpt `extra-rules` + goimports), staticcheck/gosec/revive, etc.;
generated sqlc code and migrations are excluded. Install:
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2`.
CI (`.github/workflows/ci.yml`) also runs `govulncheck` and the sqlc-sync check.
Releases are tag-driven via goreleaser — see `docs/releasing.md`.

CI/tooling gotchas:
- CI triggers on **PRs and pushes to `main`** — a feature-branch push alone won't run it; open a PR.
- Go-installed tools (`golangci-lint`, `goreleaser`, go-installed `sqlc`) live in `$(go env GOPATH)/bin`, often **not** on `PATH` — use the `make` targets (they resolve it) or full paths; `make tools` installs pinned versions.
- `goreleaser check` validates schema only, **not templates** — validate `.goreleaser.yaml` with `goreleaser release --snapshot --clean` (it catches bad fields like an invalid `{{ .IsPrerelease }}`; the engine is docker/buildx via `dockers_v2`, podman is unsupported in goreleaser ≥2.16).

### sqlc (critical, non-obvious)
Store queries are written as SQL in `internal/store/sqlite/queries/*.sql` and compiled to Go by **sqlc**. After editing any file in `queries/` **or** `migrations/`, regenerate:
```bash
make sqlc                                     # = sqlc generate
make sqlc-check                               # fail if committed sqlc code is stale (CI-enforced)
# install pinned tools (sqlc v1.31.1 + golangci-lint v2.12.2): make tools
```
Generated code in `internal/store/sqlite/sqlc/` is committed and **never hand-edited**. CI runs `make sqlc-check`'s equivalent, so regenerate and commit after touching `queries/` or `migrations/`. `sqlc.yaml` sets `emit_pointers_for_null_types: false`, so nullable columns map to `sql.NullInt64` — the mapping helpers (`nullUnix`/`ptrUnix`) depend on this.

**Exception — dynamic SQL is not sqlc:** queries with a runtime-variable shape (conditional `WHERE`, variadic `IN`, dynamic `ORDER`/keyset column) are hand-written `fmt.Sprintf` + bound-params directly in `store/sqlite/*.go`, **not** in `queries/` — e.g. `ListEntries`, `SetStatus`, `SetStarred`, `MarkReadByFilter`. sqlc only compiles static SQL, so these can't be expressed there; editing them needs **no** `make sqlc`. Safe because only the skeleton (column names, WHERE/ORDER fragments) is interpolated from a **closed code allowlist** — every value is a bound `?` — which is why the `//nolint:gosec // G201` on them is legitimate.

### Running / CLI
```bash
BFEED_LISTEN_ADDR=:8080 BFEED_BASE_URL=http://localhost:8080 BFEED_LOG_FORMAT=text go run ./cmd/bfeed serve
```
Subcommands: `serve` (default), `migrate`, `healthcheck` (for container HEALTHCHECK), `version`.

`BFEED_LISTEN_ADDR` (bind, default `:8080`) and `BFEED_BASE_URL` (external URL for links/cookies/User-Agent, **required**) are intentionally distinct — setting only `BASE_URL` does **not** change the bind port. Other env: `BFEED_DATABASE_PATH`, `BFEED_POLL_INTERVAL`, `BFEED_POLL_TICK`, `BFEED_FEED_WORKERS`, `BFEED_HOST_CONCURRENCY` (see `internal/config`).

## Architecture (ports & adapters)

Dependencies point **inward**. `internal/core` holds domain types, the services (all business logic), and the interfaces those services consume (`Store`, `Fetcher`, `FeedParser`, `Sanitizer`, `Clock`, `FeedPoller`). **Interfaces are owned by the consumer (core), not the implementer.** `core` imports **no** adapter package; adapters import `core`; `cmd/bfeed` is the only place that wires concrete adapters into services.

- **Driven adapters** implement core ports: `store/sqlite`, `fetch`, `parse`, `sanitize`, `clock`.
- **Driving adapters** call core services: `web` (htmx handlers) and the `Poller` (background scheduler).
- **Services** (`internal/core`): `FeedService` (subscribe/list/delete/refresh + `PollFeed`, which **is** the poll pipeline and satisfies `FeedPoller`), `EntryService`, `Poller` (tick → `ListDueFeeds` → bounded worker pool calling `FeedPoller.PollFeed`), `ScrapeService` (`ScrapeEntry` = the full-content pipeline fetch→extract→sanitise→`SetEntryContent`, satisfies `EntryScraper`), `Scraper` (tick → `ListPendingExtractions` → bounded pool calling `EntryScraper.ScrapeEntry`).

The poll pipeline lives in `FeedService.PollFeed` so the `Poller` only schedules; both share it. `Subscribe` does one immediate poll to populate the feed.

Full-content extraction mirrors polling: `Scraper`/`ScrapeService` are the `Poller`/`FeedService` analogue, driven by DB-backed `entries.extract_state` (`none`/`pending`/`done`/`failed`) + `next_extract_at`; the `Scraper` shares the one `Fetcher` (per-host budget) with the `Poller`.

**Web (htmx) response conventions:** per-item actions return a swapped HTML fragment (e.g. `entryrow`); bulk / whole-collection mutations return `204` + `HX-Refresh: true` (htmx does a full reload — keeps nav/sidebar unread counts consistent, no fragment targeting). List-view toolbar controls belong in the `content` block (`entries.gohtml`), **not** the `entrylist`/`entryrow` fragments (`rows.gohtml`) that htmx "load more" re-renders, or they get duplicated/lost on pagination.

**Reader-view actions** (`entry.gohtml`) carry `hx-vals='{"from":"reader"}'`; handlers branch on `r.FormValue("from")=="reader"` → mark-unread/delete reply `204`+`HX-Redirect:/` (never a fragment), star swaps the `readerstar` fragment in place. **`GET /entries/{id}` marks the entry read on open**, so a reader mutation must never re-render the reader (it would re-mark read) — that's why mark-unread/delete redirect. The `.entry` and `.actions` CSS classes are shared by the entry list **and** the feeds/categories pages — don't restyle them for one surface; the entry-list/reader icon bar uses a separate `.actbar` class. Shared template partials are wired once via the `common` slice in `parseTemplates`; the standalone `entryrow` fragment is parsed separately and lists its partials explicitly. Icons are `ic-*` inline-SVG defines in `_icons.gohtml` (currentColor, sized in CSS).

## Invariants the tests defend (don't break these)

- **Feed metadata is poll-owned.** Every successful poll **including 304** refreshes `feeds.title`/`site_url`/`description` from the parsed feed (`recordSuccess` via `orKeep`); `feedTitle` then guarantees `title` is never blank (falls back to the feed URL). Any *user* override of these fields must live in a **separate** column — writing into `feeds.title` is clobbered on the next poll (see roadmap A7, rename feed).
- **Sanitise before persistence.** Feed/extracted HTML is run through `internal/sanitize` (bluemonday allowlist runs last) before it ever reaches the DB. Entry `Content`/`Summary` in the store are always already-safe HTML; the web layer renders only that as `template.HTML`.
- **No stale read-state on Back.** Dynamic HTML is served `Cache-Control: no-store` (`noStore` middleware in `web.go`) and `layout.gohtml` reloads on `pageshow`+`event.persisted` — both defeat Safari bfcache restoring an opened-then-read entry as still-unread. Static assets keep their long cache (`cacheStatic` overrides). Tests: `TestDynamicHTMLIsNoStore`, `TestStaticAssetsKeepTheirCacheHeader`, `TestLayoutHasBfcacheReloadScript`.
- **Injected `Clock` in core.** Core/services use `clk.Now()`, never `time.Now()`. The ban is on **core**, not adapters: `store/sqlite` (persistence — `read_at`/tombstone `deleted_at`) and the `web` presentation layer (`humanizeSince` relative timestamps) deliberately read wall-clock.
- **SQLite shape:** all tables `STRICT`; timestamps `INTEGER` Unix seconds UTC; booleans `0/1` with `CHECK`; `foreign_keys=ON`; **single-writer pool** (`SetMaxOpenConns(1)`); pagination is **keyset**, never `OFFSET`, via `core.Cursor{Key int64, ID}` — `ListEntries` selects the sort column from `EntryFilter.Order` (`OrderPublishedDesc`→`published_at`; `OrderReadAtDesc`→`read_at IS NOT NULL`, the history view). A keyset partial index must carry the trailing `id DESC` tiebreak (e.g. `idx_entries_readhist`) or `EXPLAIN` shows a temp B-tree.
- **User scoping:** every *user-facing* store query is scoped by `user_id` (always `core.DefaultUserID` (1) in the MVP) — never trust an id without its owning user. **Background system sweeps are the deliberate exception** and take no `user_id`: `Poller.ListDueFeeds`, the cleaner, and the `Scraper`'s `ListPendingExtractions`/`SetEntryContent`/`UpdateExtractState` run as the system across all users — don't flag these as scoping violations.
- **Tombstones** `(feed_id, guid)` block re-poll resurrection of individually deleted / TTL-expired entries **while the feed exists**. Deleting a whole feed cascades its entries *and* tombstones away and writes none (a re-subscribe gets a fresh `feed_id`). `(feed_id, guid)` is unique; re-fetched entries upsert by content hash.
- **Politeness:** fetches use conditional GET (ETag/If-Modified-Since → 304 short-circuit), a per-host concurrency cap, and exponential backoff honoring `Retry-After`.

## Testing conventions

- TDD (red/green/refactor). **stdlib `testing` only — no testify.** Fake `Clock` for deterministic time.
- **Shared test doubles live once** in the regular package `internal/core/coretest` (exported `MemStore`, `StubFetcher`, `StubParser`, `PassSanitizer`, `StubClock`, `DiscardLogger`). Tests that use them are **external** packages (`package core_test`, `package web_test`) importing `coretest` — never redefine or copy a fake into a test package. White-box test files needing no doubles may stay `package core` (they coexist with the `core_test` files). `MemStore` is a **behavioral** fake (honors `EntryFilter.Order`/`Cursor`, sets `ReadAt` on `SetStatus`) — keep it in sync when store query semantics change, or tests pass against a fake that lies.
- `store/sqlite` is integration-tested against a real temp-file SQLite DB; hot list queries assert via `EXPLAIN QUERY PLAN` (index used, no temp B-tree).
