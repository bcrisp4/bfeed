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
- `docs/audit-2026-07.md` — the 2026-07 codebase audit (84 verified findings) organised by remediation batch; `docs/prompts/remediation-batch.md` is the copy-paste kickoff prompt for working one batch (tracked in milestone "Fable audit remediation", issues #26–#38, one PR per batch).

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
- The `skip-changelog` label only takes effect if present **when a CI run starts** — CI's `pull_request` trigger has no `labeled` type, so labelling *after* PR-open leaves the first run evaluating a label-less payload and the `changelog` gate fails. Fix: label at/before PR-open, or push a commit (an empty `synchronize` is fine — `git commit --allow-empty`) to re-fire CI with the label. Note: `git push -f` is blocked here, so re-trigger with an empty commit, not amend+force-push.
- Go-installed tools (`golangci-lint`, `goreleaser`, go-installed `sqlc`) live in `$(go env GOPATH)/bin`, often **not** on `PATH` — use the `make` targets (they resolve it) or full paths; `make tools` installs pinned versions.
- `goreleaser check` validates schema only, **not templates** — validate `.goreleaser.yaml` with `goreleaser release --snapshot --clean` (it catches bad fields like an invalid `{{ .IsPrerelease }}`; the engine is docker/buildx via `dockers_v2`, podman is unsupported in goreleaser ≥2.16).
- `Dockerfile.release` has **no build stage** — goreleaser injects prebuilt binaries at `${TARGETPLATFORM}/bfeed`. To build it locally, reconstruct that context: `mkdir -p linux/arm64 && cp <linux bfeed binary> linux/arm64/bfeed && docker build -f Dockerfile.release --build-arg TARGETPLATFORM=linux/arm64 .` (distroless has no shell — verify by running the container, not `ls`).
- Request a **Copilot PR review** via the API, not `gh pr edit --add-reviewer Copilot` (fails: "could not resolve user 'copilot'"): `gh api repos/{owner}/{repo}/pulls/{n}/requested_reviewers -f "reviewers[]=copilot-pull-request-reviewer[bot]"`. Reply in-thread via `.../pulls/{n}/comments/{id}/replies`.
- macOS `sed` (BSD) has **no `\b`** word boundary — `sed 's/\bx\b/y/'` silently matches nothing. Use the Edit tool or `perl -i -pe` for whole-word renames.
- A literal U+FEFF **BOM** char in Go source fails to compile ("illegal byte order mark" / "invalid BOM"). Use the `"﻿"` escape. The Edit tool round-trips a typed BOM back to the literal char, so insert the escape with `perl -i -pe 's/\xef\xbb\xbf/\\ufeff/'` instead.
- macOS (BSD) has **no `timeout(1)`** — `timeout 30 ./cmd` fails "command not found". Run the process in the background and `kill` it, or use a Go test with a real `net.Listener` + context deadline.
- `http.Redirect(w, r, url, code)` **panics on a nil request** — in httptest redirect handlers set `w.Header().Set("Location", url)` + `w.WriteHeader(301/302)` manually instead.
- Playwright MCP: `file://` URLs are blocked — serve mock HTML over `python3 -m http.server` and link the live app's `/static/app.css`; `browser_take_screenshot` saves to the **repo root**, not `.playwright-mcp/`.

### sqlc (critical, non-obvious)
Store queries are written as SQL in `internal/store/sqlite/queries/*.sql` and compiled to Go by **sqlc**. After editing any file in `queries/` **or** `migrations/`, regenerate:
```bash
make sqlc                                     # = sqlc generate
make sqlc-check                               # fail if committed sqlc code is stale (CI-enforced)
# install pinned tools (sqlc v1.31.1 + golangci-lint v2.12.2): make tools
```
Generated code in `internal/store/sqlite/sqlc/` is committed and **never hand-edited**. CI runs `make sqlc-check`'s equivalent, so regenerate and commit after touching `queries/` or `migrations/`. `sqlc.yaml` sets `emit_pointers_for_null_types: false`, so nullable columns map to `sql.NullInt64` — the mapping helpers (`nullUnix`/`ptrUnix`) depend on this.

**sqlc param-inference gotcha:** `BETWEEN sqlc.arg(lo) AND sqlc.arg(hi)` *inside a `CASE` expression* drops the bound args — the generated func ends up with too few params (silent; fails only at call/compile). Use explicit `>= sqlc.arg(lo) AND … <= sqlc.arg(hi)` instead, and **always eyeball the generated signature after `make sqlc`**.

**Exception — dynamic SQL is not sqlc:** queries with a runtime-variable shape (conditional `WHERE`, variadic `IN`, dynamic `ORDER`/keyset column) are hand-written `fmt.Sprintf` + bound-params directly in `store/sqlite/*.go`, **not** in `queries/` — e.g. `ListEntries`, `SetStatus`, `SetStarred`, `MarkReadByFilter`. sqlc only compiles static SQL, so these can't be expressed there; editing them needs **no** `make sqlc`. Safe because only the skeleton (column names, WHERE/ORDER fragments) is interpolated from a **closed code allowlist** — every value is a bound `?` — which is why the `//nolint:gosec // G201` on them is legitimate.

### Running / CLI
```bash
BFEED_LISTEN_ADDR=:8080 BFEED_BASE_URL=http://localhost:8080 BFEED_LOG_FORMAT=text go run ./cmd/bfeed serve
```
Subcommands: `serve` (default), `migrate`, `healthcheck` (for container HEALTHCHECK), `version`.

`BFEED_LISTEN_ADDR` (bind, default `:8080`) and `BFEED_BASE_URL` (external URL for links/cookies/User-Agent, **required**) are intentionally distinct — setting only `BASE_URL` does **not** change the bind port. Other env: `BFEED_DATABASE_PATH`, `BFEED_POLL_TICK`, `BFEED_SCHED_MIN_INTERVAL`, `BFEED_SCHED_MAX_INTERVAL`, `BFEED_SCHED_FACTOR`, `BFEED_FEED_ERROR_LIMIT`, `BFEED_FEED_WORKERS`, `BFEED_HOST_CONCURRENCY` (see `internal/config`).

## Architecture (ports & adapters)

Dependencies point **inward**. `internal/core` holds domain types, the services (all business logic), and the interfaces those services consume (`Store`, `Fetcher`, `FeedParser`, `Sanitizer`, `Clock`, `FeedPoller`). **Interfaces are owned by the consumer (core), not the implementer.** `core` imports **no** adapter package; adapters import `core`; `cmd/bfeed` is the only place that wires concrete adapters into services.

- **Driven adapters** implement core ports: `store/sqlite`, `fetch`, `parse`, `sanitize`, `clock`.
- **Driving adapters** call core services: `web` (htmx handlers) and the `Poller` (background scheduler).
- **Services** (`internal/core`): `FeedService` (subscribe/list/delete/refresh + `PollFeed`, which **is** the poll pipeline and satisfies `FeedPoller`), `EntryService`, `Poller` (tick → `ListDueFeeds` → bounded worker pool calling `FeedPoller.PollFeed`), `ScrapeService` (`ScrapeEntry` = the full-content pipeline fetch→extract→sanitise→`SetEntryContent`, satisfies `EntryScraper`), `Scraper` (tick → `ListPendingExtractions` → bounded pool calling `EntryScraper.ScrapeEntry`).

The poll pipeline lives in `FeedService.PollFeed` so the `Poller` only schedules; both share it. `Subscribe` does one immediate poll to populate the feed.

Full-content extraction mirrors polling: `Scraper`/`ScrapeService` are the `Poller`/`FeedService` analogue, driven by DB-backed `entries.extract_state` (`none`/`pending`/`done`/`failed`) + `next_extract_at`; the `Scraper` shares the one `Fetcher` (per-host budget) with the `Poller`.

**Web (htmx) response conventions:** per-item actions return a swapped HTML fragment (e.g. `entryrow`); bulk / whole-collection mutations return `204` + `HX-Refresh: true` (htmx does a full reload — keeps nav/sidebar unread counts consistent, no fragment targeting). List-view toolbar controls belong in the `content` block (`entries.gohtml`), **not** the `entrylist`/`entryrow` fragments (`rows.gohtml`) that htmx "load more" re-renders, or they get duplicated/lost on pagination. The vendored htmx is **2.0.4**, which by default does **not** swap `4xx`/`5xx` responses — an inline error fragment meant to render must return **`200`** (as `renderSubscribeError` does) or the browser silently discards it (`renderEditError`'s `422` is the B9 bug).

**Background feed ops (iter 7):** subscribe + manual refresh run in a goroutine on `context.Background()` (**not** the request ctx — it's cancelled when the handler returns), tracked by an in-memory `inflightSet` (`Handler.busy`). The feed row self-polls `GET /feeds/{id}/row` (`hx-trigger="every 1500ms"`) while in-flight and drops the trigger when done; completion piggybacks an `hx-swap-oob` group-head count update. Subscribe replies `HX-Refresh` (optimistic pending row); refresh/edit swap the row fragment. `FeedService.Subscribe` is split into `CreateSubscription` (validate+persist, no I/O) + `ResolveAndIngest` (background; records errors, no rollback); inline edit goes through `EditFeed`/`POST /feeds/{id}`. These goroutines are tracked in `Handler.bgOps` (a `sync.WaitGroup`); `web.New` returns a **`*web.Handler`** (still an `http.Handler`) whose `Drain(ctx)` `serve.go` awaits between `srv.Shutdown` and `db.Close` so a mid-flight write isn't cut off by a closed pool. A **new** `context.Background()` goroutine that isn't `Add`/`Done`-tracked in `bgOps` is silently lost on shutdown.

**Reader-view actions** (`entry.gohtml`) carry `hx-vals='{"from":"reader"}'`; handlers branch on `r.FormValue("from")=="reader"` → mark-unread/delete reply `204`+`HX-Redirect:/` (never a fragment), star swaps the `readerstar` fragment in place. **`GET /entries/{id}` marks the entry read on open**, so a reader mutation must never re-render the reader (it would re-mark read) — that's why mark-unread/delete redirect. The `.entry` and `.actions` CSS classes are shared by the entry list **and** the feeds/categories pages — don't restyle them for one surface; the entry-list/reader icon bar uses a separate `.actbar` class. Shared template partials are wired once via the `common` slice in `parseTemplates`; the standalone `entryrow` fragment is parsed separately and lists its partials explicitly. Icons are `ic-*` inline-SVG defines in `_icons.gohtml` (currentColor, sized in CSS).

## Invariants the tests defend (don't break these)

- **Background goroutines never crash the process (B3).** The untrusted-content pipeline methods (`FeedService.PollFeed`/`ResolveAndIngest`, `ScrapeService.ScrapeEntry`) `recover()` a panic and convert it to a recorded feed/entry error → backoff reschedule (not a re-trip-every-tick busy loop). Every long-lived background goroutine (poller/scraper workers, web subscribe/refresh) also `defer core.RecoverGuard(...)` as a backstop. Any **new** background worker over feed/article content must do the same — net/http only recovers panics in request handlers.
- **Feed metadata is poll-owned.** Every successful poll **including 304** refreshes `feeds.title`/`site_url`/`description` from the parsed feed (`recordSuccess` via `orKeep`); `feedTitle` then guarantees `title` is never blank (falls back to the feed URL). Any *user* override of these fields must live in a **separate** column — writing into `feeds.title` is clobbered on the next poll. **Done (iter 7):** `feeds.user_title` override + `Feed.DisplayTitle()` (= `user_title ?? title`); the web layer must render `DisplayTitle()` **everywhere** a feed name shows (`feedTitleMap`, `singleFeedTitle`, feed rows), never raw `f.Title`.
- **Sanitise before persistence.** Feed/extracted HTML is run through `internal/sanitize` (bluemonday allowlist runs last) before it ever reaches the DB. Entry `Content`/`Summary` in the store are always already-safe HTML; the web layer renders only that as `template.HTML`.
- **Feed decoding (B4).** `FeedParser.Parse` takes the HTTP `Content-Type`; `parse.decodeReader` pre-transcodes non-UTF-8 feeds via `x/net/html/charset` **only when there's no in-band `encoding=` decl**. gofeed/goxpp honors **only an adjacent lowercase `encoding="…"`** — a spaced (`encoding = "…"`) or case-variant decl is NOT transcoded by gofeed, so the adjacent-substring check is deliberately congruent with it (skip our wrap exactly when gofeed transcodes → never double-transcode). Don't "tighten" it to detect spaced/cased decls — it regresses the spaced form. Undated entries get ingest-time `PublishedAt` in `FeedService.ingest` (never the zero year-1 value).
- **No stale read-state on Back.** Dynamic HTML is served `Cache-Control: no-store` (`noStore` middleware in `web.go`) and `layout.gohtml` reloads on `pageshow`+`event.persisted` — both defeat Safari bfcache restoring an opened-then-read entry as still-unread. Static assets keep their long cache (`cacheStatic` overrides). Tests: `TestDynamicHTMLIsNoStore`, `TestStaticAssetsKeepTheirCacheHeader`, `TestLayoutHasBfcacheReloadScript`.
- **Injected `Clock` in core.** Core/services use `clk.Now()`, never `time.Now()`. The ban is on **core**, not adapters: `store/sqlite` (persistence — `read_at`/tombstone `deleted_at`) and the `web` presentation layer (`humanizeSince` relative timestamps) deliberately read wall-clock.
- **SQLite shape:** all tables `STRICT`; timestamps `INTEGER` Unix seconds UTC; booleans `0/1` with `CHECK`; `foreign_keys=ON`; **single-writer pool** (`SetMaxOpenConns(1)`); pagination is **keyset**, never `OFFSET`, via `core.Cursor{Key int64, ID}` — `ListEntries` selects the sort column from `EntryFilter.Order` (`OrderPublishedDesc`→`published_at`; `OrderReadAtDesc`→`read_at IS NOT NULL`, the history view). A keyset partial index must carry the trailing `id DESC` tiebreak (e.g. `idx_entries_readhist`) or `EXPLAIN` shows a temp B-tree.
- **User scoping:** every *user-facing* store query is scoped by `user_id` (always `core.DefaultUserID` (1) in the MVP) — never trust an id without its owning user. **Background system sweeps are the deliberate exception** and take no `user_id`: `Poller.ListDueFeeds`, the cleaner, and the `Scraper`'s `ListPendingExtractions`/`SetEntryContent`/`UpdateExtractState` run as the system across all users — don't flag these as scoping violations.
- **Tombstones** `(feed_id, guid)` block re-poll resurrection of individually deleted / TTL-expired entries **while the feed exists**. Deleting a whole feed cascades its entries *and* tombstones away and writes none (a re-subscribe gets a fresh `feed_id`). `(feed_id, guid)` is unique; re-fetched entries upsert by content hash.
- **Politeness:** fetches use conditional GET (ETag/If-Modified-Since → 304 short-circuit), a per-host concurrency cap, and exponential backoff honoring `Retry-After`.
- **Redirects (B5):** the fetch client follows ≤5 redirects and exposes `FetchResponse.FinalURL` (post-redirect URL) + `PermanentRedirect` (true iff ≥1 hop and *every* hop was 301/308; tracked per-request via a ctx-scoped `redirectTracker` since `CheckRedirect` is client-shared). Resolve relative links against `FinalURL`, never the pre-redirect URL: scrape base (`ScrapeEntry`), autodiscovery + feed `Parse` (`resolveFeed`), and `PollFeed`'s parse base. `PollFeed`/`resolveFeed` adopt `FinalURL` as `feeds.feed_url` **only** on a fully-permanent chain (a 302/307 must not rewrite identity); a poll-time `SetFeedURL` `ErrConflict` keeps the old URL and does **not** fail the poll (subscribe records it as an error instead). The per-host semaphore key is normalized (`hostKey`: lowercased host, default port stripped) but acquired for the **origin** host — a cross-host redirect still counts against the origin's budget (deliberate residual; don't re-flag).
- **Adaptive scheduling (iter 6):** the success-path interval is `core.AdaptiveInterval` (pure, `schedule.go`) = `week / (weeklyCount * factor)` clamped to `[min, max]`, where `weeklyCount` is `Store.WeeklyEntryCount` — a `COUNT` over `[now-week, now]` that falls back to `created_at` when `published_at` is absent. A feed younger than a week polls at `MinInterval` (cold start). Publisher TTL (`feeds.ttl_seconds`, poll-owned, from RSS `<ttl>`/`sy:*`) raises the interval, capped at 30d. The error path is unchanged (`PollReschedule` backoff). There is **no** hard error-limit dispatch exclusion — `BFEED_FEED_ERROR_LIMIT` only drives the Feeds-page "stalled" badge.

## Testing conventions

- TDD (red/green/refactor). **stdlib `testing` only — no testify.** Fake `Clock` for deterministic time.
- **Shared test doubles live once** in the regular package `internal/core/coretest` (exported `MemStore`, `StubFetcher`, `StubParser`, `PassSanitizer`, `StubClock`, `DiscardLogger`). Tests that use them are **external** packages (`package core_test`, `package web_test`) importing `coretest` — never redefine or copy a fake into a test package. White-box test files needing no doubles may stay `package core` (they coexist with the `core_test` files). `MemStore` is a **behavioral** fake (honors `EntryFilter.Order`/`Cursor`, sets `ReadAt` on `SetStatus`) — keep it in sync when store query semantics change, or tests pass against a fake that lies.
- `store/sqlite` is integration-tested against a real temp-file SQLite DB; hot list queries assert via `EXPLAIN QUERY PLAN` (index used, no temp B-tree).
