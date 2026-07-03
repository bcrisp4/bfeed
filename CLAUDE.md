# CLAUDE.md

Guide Claude Code (claude.ai/code) for work in this repo.

## What this is

bfeed — self-hosted RSS/Atom/JSON feed reader: one pure-Go binary (`CGO_ENABLED=0`, no cgo), one SQLite file, htmx UI. Single-user MVP (no auth; tailnet is boundary). Module `github.com/bcrisp4/bfeed`, Go 1.25+.

Design documented + authoritative — read before non-trivial work:
- `docs/design.md` — full north-star spec (long-term target).
- `docs/mvp-design.md` — scope **actually built** now (iteration 1). When code and `design.md` disagree, this is why.
- `docs/roadmap.md` — everything deferred, with additive path back.
- `docs/releasing.md` — how to cut release (annotated semver tag → goreleaser).
- `docs/changelog.md` — changelog policy (what, when, how CI enforces).
- `docs/audit-2026-07.md` — 2026-07 codebase audit (84 verified findings) by remediation batch; `docs/prompts/remediation-batch.md` is copy-paste kickoff prompt for one batch (milestone "Fable audit remediation", issues #26–#38, one PR per batch).

(Plans under `docs/superpowers/plans/` gitignored, per "don't commit plans" rule. `bfeed.db` + WAL files in repo root gitignored local dev state.)

## Changelog (mandatory)

`CHANGELOG.md` ([Keep a Changelog](https://keepachangelog.com/en/1.1.0/)) is single
source of truth for release notes. **Every PR that changes behaviour must
add entry under `[Unreleased]`** — CI's `changelog` job fails PR otherwise.
Write entries from user perspective under right category (`Added`,
`Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`). PRs with no user-facing
change (CI/tooling, pure refactors, test-only, deps, docs) carry
`skip-changelog` label instead. Full policy + release-time
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
golangci-lint v2 (config `.golangci.yml`) is lint bar — runs `go vet`,
formatting (gofumpt `extra-rules` + goimports), staticcheck/gosec/revive, etc.;
generated sqlc code + migrations excluded. Install:
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2`.
CI (`.github/workflows/ci.yml`) also runs `govulncheck` + sqlc-sync check.
Releases tag-driven via goreleaser — see `docs/releasing.md`.

CI/tooling gotchas:
- CI triggers on **PRs and pushes to `main`** — feature-branch push alone won't run; open PR.
- `skip-changelog` label only takes effect if present **when CI run starts** — CI's `pull_request` trigger has no `labeled` type, so labelling *after* PR-open leaves first run evaluating label-less payload and `changelog` gate fails. Fix: label at/before PR-open, or push commit (empty `synchronize` fine — `git commit --allow-empty`) to re-fire CI with label. Note: `git push -f` blocked here, so re-trigger with empty commit, not amend+force-push.
- Go-installed tools (`golangci-lint`, `goreleaser`, go-installed `sqlc`) live in `$(go env GOPATH)/bin`, often **not** on `PATH` — use `make` targets (they resolve it) or full paths; `make tools` installs pinned versions.
- `goreleaser check` validates schema only, **not templates** — validate `.goreleaser.yaml` with `goreleaser release --snapshot --clean` (catches bad fields like invalid `{{ .IsPrerelease }}`; engine is docker/buildx via `dockers_v2`, podman unsupported in goreleaser ≥2.16).
- `Dockerfile.release` has **no build stage** — goreleaser injects prebuilt binaries at `${TARGETPLATFORM}/bfeed`. Build locally: reconstruct context: `mkdir -p linux/arm64 && cp <linux bfeed binary> linux/arm64/bfeed && docker build -f Dockerfile.release --build-arg TARGETPLATFORM=linux/arm64 .` (distroless has no shell — verify by running container, not `ls`).
- Request **Copilot PR review** via API, not `gh pr edit --add-reviewer Copilot` (fails: "could not resolve user 'copilot'"): `gh api repos/{owner}/{repo}/pulls/{n}/requested_reviewers -f "reviewers[]=copilot-pull-request-reviewer[bot]"`. Reply in-thread via `.../pulls/{n}/comments/{id}/replies`.
- macOS `sed` (BSD) has **no `\b`** word boundary — `sed 's/\bx\b/y/'` silently matches nothing. Use Edit tool or `perl -i -pe` for whole-word renames.
- Literal U+FEFF **BOM** char in Go source fails to compile ("illegal byte order mark" / "invalid BOM"). Use `"﻿"` escape. Edit tool round-trips typed BOM back to literal char, so insert escape with `perl -i -pe 's/\xef\xbb\xbf/\\ufeff/'` instead.
- macOS (BSD) has **no `timeout(1)`** — `timeout 30 ./cmd` fails "command not found". Run process in background + `kill` it, or use Go test with real `net.Listener` + context deadline.
- `http.Redirect(w, r, url, code)` **panics on nil request** — in httptest redirect handlers set `w.Header().Set("Location", url)` + `w.WriteHeader(301/302)` manually instead.
- Playwright MCP: `file://` URLs blocked — serve mock HTML over `python3 -m http.server` and link live app's `/static/app.css`; `browser_take_screenshot` saves to **repo root**, not `.playwright-mcp/`.

### sqlc (critical, non-obvious)
Store queries written as SQL in `internal/store/sqlite/queries/*.sql`, compiled to Go by **sqlc**. After editing any file in `queries/` **or** `migrations/`, regenerate:
```bash
make sqlc                                     # = sqlc generate
make sqlc-check                               # fail if committed sqlc code is stale (CI-enforced)
# install pinned tools (sqlc v1.31.1 + golangci-lint v2.12.2): make tools
```
Generated code in `internal/store/sqlite/sqlc/` committed and **never hand-edited**. CI runs `make sqlc-check` equivalent, so regenerate + commit after touching `queries/` or `migrations/`. `sqlc.yaml` sets `emit_pointers_for_null_types: false`, so nullable columns map to `sql.NullInt64` — mapping helpers (`nullUnix`/`ptrUnix`) depend on this.

**sqlc param-inference gotcha:** `BETWEEN sqlc.arg(lo) AND sqlc.arg(hi)` *inside `CASE` expression* drops bound args — generated func ends up with too few params (silent; fails only at call/compile). Use explicit `>= sqlc.arg(lo) AND … <= sqlc.arg(hi)` instead, and **always eyeball generated signature after `make sqlc`**.

**Non-ASCII in `queries/*.sql` silently corrupts generated SQL:** a multibyte char (e.g. em-dash `—` U+2014) anywhere in a query file — even a `--` comment — throws off sqlc's byte tracking, relocating each subsequent query's trailing `?;` to the top of its generated const (leaving `WHERE … =` danglingly incomplete). `go build` passes (valid Go string); fails only at query time as "SQL logic error: incomplete input". Keep query files ASCII-only; scan after editing with `grep -nP '[^\x00-\x7F]' internal/store/sqlite/queries/*.sql`.

**Table-rebuild migrations** (changing a PK to `AUTOINCREMENT`, or any `CREATE new / copy / DROP old / RENAME`) must use `-- +goose NO TRANSACTION` + explicit `PRAGMA foreign_keys=OFF; … PRAGMA foreign_keys=ON;`. The DSN sets `foreign_keys(ON)`, so a `DROP TABLE` of a **parent** (e.g. `feeds`) runs an implicit cascade that wipes children (`entries`/`tombstones`); and `PRAGMA foreign_keys` is a **no-op inside a transaction**, so it only bites in NO TRANSACTION mode. Carry ids over explicitly (`INSERT INTO new (id,…) SELECT id,… FROM old`) to preserve identity + FK refs; recreate the table's indexes after `RENAME`. Verify with an upgrade-path test (goose `UpTo(…, N-1)`, seed rows, `UpTo(…, N)`, assert data survives). See `0010_feeds_autoincrement.sql`.

**Exception — dynamic SQL is not sqlc:** queries with runtime-variable shape (conditional `WHERE`, variadic `IN`, dynamic `ORDER`/keyset column) are hand-written `fmt.Sprintf` + bound-params directly in `store/sqlite/*.go`, **not** in `queries/` — e.g. `ListEntries`, `SetStatus`, `SetStarred`, `MarkReadByFilter`. sqlc only compiles static SQL, so these can't live there; editing them needs **no** `make sqlc`. Safe because only skeleton (column names, WHERE/ORDER fragments) interpolated from **closed code allowlist** — every value is bound `?` — why the `//nolint:gosec // G201` on them is legitimate. **One deliberate value exception:** `ListEntries` interpolates a *literal* `e.starred = 0/1` (via `b2i`), not a bound `?`, because a **partial index** (`idx_entries_starred WHERE starred = 1`) is unusable by `starred = ?` — SQLite needs a literal to prove the partial predicate holds. Bounded 0/1 literal, still G201-safe.

### Running / CLI
```bash
BFEED_LISTEN_ADDR=:8080 BFEED_BASE_URL=http://localhost:8080 BFEED_LOG_FORMAT=text go run ./cmd/bfeed serve
```
Subcommands: `serve` (default), `migrate`, `healthcheck` (for container HEALTHCHECK), `version`.

`BFEED_LISTEN_ADDR` (bind, default `:8080`) and `BFEED_BASE_URL` (external URL for links/cookies/User-Agent, **required**) intentionally distinct — setting only `BASE_URL` does **not** change bind port. Other env: `BFEED_DATABASE_PATH`, `BFEED_POLL_TICK`, `BFEED_SCHED_MIN_INTERVAL`, `BFEED_SCHED_MAX_INTERVAL`, `BFEED_SCHED_FACTOR`, `BFEED_FEED_ERROR_LIMIT`, `BFEED_FEED_WORKERS`, `BFEED_HOST_CONCURRENCY` (see `internal/config`).

**SSRF guard (B11) blocks loopback/private fetch targets:** subscribing to a `localhost`/`127.0.0.1`/`::1` feed fails "ssrf guard: blocked address", so you **can't** e2e-test feed ingest against a local `python3 -m http.server`. Rely on unit tests (fake `Fetcher`) for the ingest pipeline; a running `serve` only confirms wiring/migration, not real fetch.

## Architecture (ports & adapters)

Dependencies point **inward**. `internal/core` holds domain types, services (all business logic), and interfaces those services consume (`Store`, `Fetcher`, `FeedParser`, `Sanitizer`, `Clock`, `FeedPoller`). **Interfaces owned by consumer (core), not implementer.** `core` imports **no** adapter package; adapters import `core`; `cmd/bfeed` is only place wiring concrete adapters into services.

- **Driven adapters** implement core ports: `store/sqlite`, `fetch`, `parse`, `sanitize`, `clock`.
- **Driving adapters** call core services: `web` (htmx handlers) + `Poller` (background scheduler).
- **Services** (`internal/core`): `FeedService` (subscribe/list/delete/refresh + `PollFeed`, which **is** poll pipeline and satisfies `FeedPoller`), `EntryService`, `Poller` (tick → `ListDueFeeds` → bounded worker pool calling `FeedPoller.PollFeed`), `ScrapeService` (`ScrapeEntry` = full-content pipeline fetch→extract→sanitise→`SetEntryContent`, satisfies `EntryScraper`), `Scraper` (tick → `ListPendingExtractions` → bounded pool calling `EntryScraper.ScrapeEntry`).

Poll pipeline lives in `FeedService.PollFeed` so `Poller` only schedules; both share it. `Subscribe` does one immediate poll to populate feed.

Full-content extraction mirrors polling: `Scraper`/`ScrapeService` are `Poller`/`FeedService` analogue, driven by DB-backed `entries.extract_state` (`none`/`pending`/`done`/`failed`) + `next_extract_at`; `Scraper` shares the one `Fetcher` (per-host budget) with `Poller`.

**Web (htmx) response conventions:** per-item actions return swapped HTML fragment (e.g. `entryrow`); bulk / whole-collection mutations return `204` + `HX-Refresh: true` (htmx does full reload — keeps nav/sidebar unread counts consistent, no fragment targeting). List-view toolbar controls belong in `content` block (`entries.gohtml`), **not** the `entrylist`/`entryrow` fragments (`rows.gohtml`) that htmx "load more" re-renders, or they get duplicated/lost on pagination. Vendored htmx is **2.0.4**, which by default does **not** swap `4xx`/`5xx` responses — inline error fragment meant to render must return **`200`** (as `renderSubscribeError` does) or browser silently discards it (`renderEditError`'s `422` is the B9 bug).

**Background feed ops (iter 7):** subscribe + manual refresh run in goroutine on `context.Background()` (**not** request ctx — cancelled when handler returns), tracked by in-memory `inflightSet` (`Handler.busy`). Feed row self-polls `GET /feeds/{id}/row` (`hx-trigger="every 1500ms"`) while in-flight, drops trigger when done; completion piggybacks `hx-swap-oob` group-head count update. Subscribe replies `HX-Refresh` (optimistic pending row); refresh/edit swap row fragment. `FeedService.Subscribe` split into `CreateSubscription` (validate+persist, no I/O) + `ResolveAndIngest` (background; records errors, no rollback); inline edit goes through `EditFeed`/`POST /feeds/{id}`. These goroutines tracked in `Handler.bgOps` (a `sync.WaitGroup`); `web.New` returns **`*web.Handler`** (still an `http.Handler`) whose `Drain(ctx)` `serve.go` awaits between `srv.Shutdown` and `db.Close` so mid-flight write isn't cut off by closed pool. A **new** `context.Background()` goroutine not `Add`/`Done`-tracked in `bgOps` is silently lost on shutdown.

**Reader-view actions** (`entry.gohtml`) carry `hx-vals='{"from":"reader"}'`; handlers branch on `r.FormValue("from")=="reader"` → mark-unread/delete reply `204`+`HX-Redirect:/` (never fragment), star swaps `readerstar` fragment in place. **`GET /entries/{id}` marks entry read on open**, so reader mutation must never re-render reader (would re-mark read) — why mark-unread/delete redirect. `.entry` and `.actions` CSS classes shared by entry list **and** feeds/categories pages — don't restyle for one surface; entry-list/reader icon bar uses separate `.actbar` class. Shared template partials wired once via `common` slice in `parseTemplates`; standalone `entryrow` fragment parsed separately, lists its partials explicitly. Icons are `ic-*` inline-SVG defines in `_icons.gohtml` (currentColor, sized in CSS).

## Invariants the tests defend (don't break these)

- **Background goroutines never crash process (B3).** Untrusted-content pipeline methods (`FeedService.PollFeed`/`ResolveAndIngest`, `ScrapeService.ScrapeEntry`) `recover()` a panic and convert to recorded feed/entry error → backoff reschedule (not re-trip-every-tick busy loop). Every long-lived background goroutine (poller/scraper workers, web subscribe/refresh) also `defer core.RecoverGuard(...)` as backstop. Any **new** background worker over feed/article content must do same — net/http only recovers panics in request handlers.
- **Feed metadata is poll-owned.** Every successful poll **including 304** refreshes `feeds.title`/`site_url`/`description` from parsed feed (`recordSuccess` via `orKeep`); `feedTitle` then guarantees `title` never blank (falls back to feed URL). Any *user* override of these fields must live in **separate** column — writing into `feeds.title` clobbered on next poll. **Done (iter 7):** `feeds.user_title` override + `Feed.DisplayTitle()` (= `user_title ?? title`); web layer must render `DisplayTitle()` **everywhere** a feed name shows (`feedTitleMap`, `singleFeedTitle`, feed rows), never raw `f.Title`.
- **Sanitise before persistence.** Feed/extracted HTML run through `internal/sanitize` (bluemonday allowlist runs last) before it ever reaches DB. Entry `Content`/`Summary` in store always already-safe HTML; web layer renders only that as `template.HTML`.
- **Feed decoding (B4).** `FeedParser.Parse` takes HTTP `Content-Type`; `parse.decodeReader` pre-transcodes non-UTF-8 feeds via `x/net/html/charset` **only when no in-band `encoding=` decl**. gofeed/goxpp honors **only adjacent lowercase `encoding="…"`** — spaced (`encoding = "…"`) or case-variant decl NOT transcoded by gofeed, so adjacent-substring check deliberately congruent (skip our wrap exactly when gofeed transcodes → never double-transcode). Don't "tighten" to detect spaced/cased decls — regresses spaced form. Undated entries get ingest-time `PublishedAt` in `FeedService.ingest` (never zero year-1 value).
- **No stale read-state on Back.** Dynamic HTML served `Cache-Control: no-store` (`noStore` middleware in `web.go`) and `layout.gohtml` reloads on `pageshow`+`event.persisted` — both defeat Safari bfcache restoring opened-then-read entry as still-unread. Static assets keep long cache (`cacheStatic` overrides). Tests: `TestDynamicHTMLIsNoStore`, `TestStaticAssetsKeepTheirCacheHeader`, `TestLayoutHasBfcacheReloadScript`.
- **Injected `Clock` in core.** Core/services use `clk.Now()`, never `time.Now()`. Ban on **core**, not adapters: `store/sqlite` (persistence — `read_at`/tombstone `deleted_at`) and `web` presentation layer (`humanizeSince` relative timestamps) deliberately read wall-clock.
- **SQLite shape:** all tables `STRICT`; timestamps `INTEGER` Unix seconds UTC; booleans `0/1` with `CHECK`; `foreign_keys=ON`; **single-writer pool** (`SetMaxOpenConns(1)`); pagination **keyset**, never `OFFSET`, via `core.Cursor{Key int64, ID}` — `ListEntries` selects sort column from `EntryFilter.Order` (`OrderPublishedDesc`→`published_at`; `OrderReadAtDesc`→`read_at IS NOT NULL`, history view). Keyset partial index must carry trailing `id DESC` tiebreak (e.g. `idx_entries_readhist`) or `EXPLAIN` shows temp B-tree. A *dropped* tiebreak emits `USE TEMP B-TREE FOR LAST TERM OF ORDER BY` (partial-sort variant), so EXPLAIN guard tests must assert the broad substring `USE TEMP B-TREE`, not `…FOR ORDER BY`.
- **User scoping:** every *user-facing* store query scoped by `user_id` (always `core.DefaultUserID` (1) in MVP) — never trust id without owning user. **Background system sweeps are deliberate exception** and take no `user_id`: `Poller.ListDueFeeds`, cleaner, and `Scraper`'s `ListPendingExtractions`/`SetEntryContent`/`UpdateExtractState` run as system across all users — don't flag as scoping violations.
- **Tombstones** `(feed_id, guid)` block re-poll resurrection of individually deleted / TTL-expired entries **while feed exists**. Deleting whole feed cascades its entries *and* tombstones away, writes none (re-subscribe gets fresh `feed_id`). `(feed_id, guid)` unique; re-fetched entries upsert by content hash.
- **Politeness:** fetches use conditional GET (ETag/If-Modified-Since → 304 short-circuit), per-host concurrency cap, exponential backoff honoring `Retry-After`.
- **Redirects (B5):** fetch client follows ≤5 redirects, exposes `FetchResponse.FinalURL` (post-redirect URL) + `PermanentRedirect` (true iff ≥1 hop and *every* hop was 301/308; tracked per-request via ctx-scoped `redirectTracker` since `CheckRedirect` is client-shared). Resolve relative links against `FinalURL`, never pre-redirect URL: scrape base (`ScrapeEntry`), autodiscovery + feed `Parse` (`resolveFeed`), `PollFeed`'s parse base, and entry-content sanitise base (`ingest` takes `orURL(resp.FinalURL, f.FeedURL)` — independent of identity adoption, so a *temporary* redirect still resolves content links against the final URL without rewriting `feed_url`). `PollFeed`/`resolveFeed` adopt `FinalURL` as `feeds.feed_url` **only** on fully-permanent chain (302/307 must not rewrite identity); poll-time `SetFeedURL` `ErrConflict` keeps old URL and does **not** fail poll (subscribe records it as error instead). Per-host semaphore key normalized (`hostKey`: lowercased host, default port stripped) but acquired for **origin** host — cross-host redirect still counts against origin's budget (deliberate residual; don't re-flag).
- **Adaptive scheduling (iter 6):** success-path interval is `core.AdaptiveInterval` (pure, `schedule.go`) = `week / (weeklyCount * factor)` clamped to `[min, max]`, where `weeklyCount` is `Store.WeeklyEntryCount` — a `COUNT` over `[now-week, now]` falling back to `created_at` when `published_at` absent. Feed younger than week polls at `MinInterval` (cold start). Publisher TTL (`feeds.ttl_seconds`, poll-owned, from RSS `<ttl>`/`sy:*`) raises interval, capped at 30d. Error path unchanged (`PollReschedule` backoff). **No** hard error-limit dispatch exclusion — `BFEED_FEED_ERROR_LIMIT` only drives Feeds-page "stalled" badge.

## Testing conventions

- TDD (red/green/refactor). **stdlib `testing` only — no testify.** Fake `Clock` for deterministic time.
- **Shared test doubles live once** in regular package `internal/core/coretest` (exported `MemStore`, `StubFetcher`, `StubParser`, `PassSanitizer`, `StubClock`, `DiscardLogger`). Tests using them are **external** packages (`package core_test`, `package web_test`) importing `coretest` — never redefine or copy fake into test package. White-box test files needing no doubles may stay `package core` (coexist with `core_test` files). `MemStore` is **behavioral** fake (honors `EntryFilter.Order`/`Cursor`, sets `ReadAt` on `SetStatus`) — keep in sync when store query semantics change, or tests pass against fake that lies.
- `store/sqlite` integration-tested against real temp-file SQLite DB; hot list queries assert via `EXPLAIN QUERY PLAN` (index used, no temp B-tree).
