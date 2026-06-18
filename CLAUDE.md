# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

bfeed — a self-hosted RSS/Atom/JSON feed reader: one pure-Go binary (`CGO_ENABLED=0`, no cgo), one SQLite file, htmx UI. Single-user MVP (no auth; tailnet is the boundary). Module `github.com/bcrisp4/bfeed`, Go 1.25+.

The design is documented and authoritative — read it before non-trivial work:
- `docs/design.md` — full north-star spec (the long-term target).
- `docs/mvp-design.md` — the scope that is **actually built** right now (iteration 1). When code and `design.md` disagree, this is why.
- `docs/roadmap.md` — everything deliberately deferred, with the additive path back.

(Implementation plans under `docs/superpowers/plans/` are gitignored, per the user's "don't commit plans" rule. `bfeed.db` and WAL files in the repo root are gitignored local dev state.)

## Commands

```bash
CGO_ENABLED=0 go build ./...                 # build (must stay cgo-free)
go test ./...                                # all tests
go test ./... -race                          # race detector (use before declaring done)
go test ./internal/core/ -run TestName -v    # a single test
go vet ./... && gofmt -l .                   # gofmt -l must print nothing
go run ./cmd/bfeed serve                     # run (needs BFEED_BASE_URL, see below)
```

### sqlc (critical, non-obvious)
Store queries are written as SQL in `internal/store/sqlite/queries/*.sql` and compiled to Go by **sqlc**. After editing any file in `queries/` **or** `migrations/`, regenerate:
```bash
sqlc generate                                # or: $(go env GOPATH)/bin/sqlc generate
# install once: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```
Generated code in `internal/store/sqlite/sqlc/` is committed and **never hand-edited**. `sqlc.yaml` sets `emit_pointers_for_null_types: false`, so nullable columns map to `sql.NullInt64` — the mapping helpers (`nullUnix`/`ptrUnix`) depend on this.

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
- **Services** (`internal/core`): `FeedService` (subscribe/list/delete/refresh + `PollFeed`, which **is** the poll pipeline and satisfies `FeedPoller`), `EntryService`, `Poller` (tick → `ListDueFeeds` → bounded worker pool calling `FeedPoller.PollFeed`).

The poll pipeline lives in `FeedService.PollFeed` so the `Poller` only schedules; both share it. `Subscribe` does one immediate poll to populate the feed.

## Invariants the tests defend (don't break these)

- **Sanitise before persistence.** Feed/extracted HTML is run through `internal/sanitize` (bluemonday allowlist runs last) before it ever reaches the DB. Entry `Content`/`Summary` in the store are always already-safe HTML; the web layer renders only that as `template.HTML`.
- **Injected `Clock` in core.** Core/services use `clk.Now()`, never `time.Now()`. The `store/sqlite` adapter is the one deliberate exception (wall-clock for `read_at`/tombstone `deleted_at` — a persistence detail, not domain logic).
- **SQLite shape:** all tables `STRICT`; timestamps `INTEGER` Unix seconds UTC; booleans `0/1` with `CHECK`; `foreign_keys=ON`; **single-writer pool** (`SetMaxOpenConns(1)`); pagination is **keyset** on `(published_at, id)`, never `OFFSET`.
- **User scoping:** every store query is scoped by `user_id`, always `core.DefaultUserID` (1) in the MVP. Never trust an id without its owning user.
- **Tombstones** `(feed_id, guid)` block re-poll resurrection of individually deleted / TTL-expired entries **while the feed exists**. Deleting a whole feed cascades its entries *and* tombstones away and writes none (a re-subscribe gets a fresh `feed_id`). `(feed_id, guid)` is unique; re-fetched entries upsert by content hash.
- **Politeness:** fetches use conditional GET (ETag/If-Modified-Since → 304 short-circuit), a per-host concurrency cap, and exponential backoff honoring `Retry-After`.

## Testing conventions

- TDD (red/green/refactor). **stdlib `testing` only — no testify.** Fake `Clock` for deterministic time.
- **Shared test doubles live once** in the regular package `internal/core/coretest` (exported `MemStore`, `StubFetcher`, `StubParser`, `PassSanitizer`, `StubClock`, `DiscardLogger`). Tests that use them are **external** packages (`package core_test`, `package web_test`) importing `coretest` — never redefine or copy a fake into a test package. White-box test files needing no doubles may stay `package core` (they coexist with the `core_test` files).
- `store/sqlite` is integration-tested against a real temp-file SQLite DB; hot list queries assert via `EXPLAIN QUERY PLAN` (index used, no temp B-tree).
