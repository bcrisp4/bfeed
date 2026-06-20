# bfeed — Full-Content Extraction (design / spec)

> Status: approved design for iteration 4 ("Content quality"), per `docs/roadmap.md` §A3.
> North-star reference: `docs/design.md` §10.4, §13. This spec records the **deliberately
> simpler, DB-backed** realisation chosen for this iteration, and the deviations from the
> north star (which stay recorded in `roadmap.md`).

## 1. Goal

Per-feed, opt-in **full-article extraction**: for feeds the user flags, bfeed fetches each
entry's article page, extracts the main content (Readability-style), sanitises it, and
**replaces the feed-provided `content`** with the full article. Feed-provided content is the
fallback whenever extraction is not wanted or fails.

Single `content` field is overwritten (matches `design.md` §8 `SetEntryContent`); the original
feed snippet is not separately preserved.

## 2. Decisions (from brainstorming)

- **Decoupled via DB-backed extraction state**, not an in-memory queue/pool. Entries carry an
  extraction state column; a background sweep drains `pending` entries. Durable across
  restarts, naturally rate-limited, no lost queue.
- **Opt-in entry points**: checkbox on the add-feed form **and** an htmx per-feed toggle.
- **Enabling on an existing feed backfills _all_ that feed's existing entries** (read or not).
- **Failure handling**: bounded retries with exponential backoff + jitter; after a cap, mark
  the entry's extraction terminally `failed` and keep the feed-provided content.

## 3. Data model & state lifecycle

### Migration `0005_full_content.sql` (goose up/down)

```sql
-- +goose Up
ALTER TABLE feeds ADD COLUMN fetch_full_content INTEGER NOT NULL DEFAULT 0
  CHECK (fetch_full_content IN (0,1));

ALTER TABLE entries ADD COLUMN extract_state TEXT NOT NULL DEFAULT 'none'
  CHECK (extract_state IN ('none','pending','done','failed'));
ALTER TABLE entries ADD COLUMN extract_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE entries ADD COLUMN next_extract_at INTEGER;   -- nullable; retry spacing

-- drain query: pending + due, freshest-first. Partial index stays tiny (only unextracted rows),
-- and is pre-sorted published_at DESC so the sweep needs no temp B-tree.
CREATE INDEX idx_entries_pending ON entries(published_at DESC) WHERE extract_state = 'pending';

-- +goose Down
DROP INDEX idx_entries_pending;
-- SQLite ALTER ... DROP COLUMN (>=3.35) — drop in reverse:
ALTER TABLE entries DROP COLUMN next_extract_at;
ALTER TABLE entries DROP COLUMN extract_attempts;
ALTER TABLE entries DROP COLUMN extract_state;
ALTER TABLE feeds DROP COLUMN fetch_full_content;
```

(All columns INTEGER/TEXT only — STRICT-safe. Booleans `0/1` with `CHECK`. Confirm the running
`modernc.org/sqlite` version supports `ALTER TABLE ... DROP COLUMN`; if not, the Down rebuilds
the table. Up is the path that matters operationally.)

### State machine (per entry)

`none` (default; no extraction wanted) → `pending` → `done` | `failed`.

| Transition | When | Effect |
|---|---|---|
| → `pending` (insert) | entry inserted into a `fetch_full_content=1` feed | `extract_state='pending'`, `next_extract_at=now` |
| `none`/`failed` → `pending` (backfill) | feed toggled **on** | `UPDATE entries SET extract_state='pending', next_extract_at=now WHERE feed_id=? AND extract_state IN ('none','failed')` — **all** existing entries, read or not; skips `done`/`pending` |
| `pending` → `none` (cancel) | feed toggled **off** | `UPDATE entries SET extract_state='none' WHERE feed_id=? AND extract_state='pending'`; `done` stays `done` |
| `pending` → `done` | extraction succeeds | content replaced, state `done` |
| `pending` → `pending` (retry) | extraction fails, attempts < cap | `extract_attempts++`, `next_extract_at = now + backoff` |
| `pending` → `failed` | extraction fails, attempts ≥ cap | terminal; feed content kept |

In-place entry updates (feed re-publish / hash change) **do not** touch the extract columns —
kept simple this iteration.

### Type changes (`internal/core/types.go`)

- `Feed` gains `FetchFullContent bool`.
- `Entry` gains `ExtractState ExtractState` (typed) and `ExtractAttempts int`:
  ```go
  type ExtractState string
  const (
      ExtractNone    ExtractState = "none"
      ExtractPending ExtractState = "pending"
      ExtractDone    ExtractState = "done"
      ExtractFailed  ExtractState = "failed"
  )
  ```
  `next_extract_at` is a store-internal scheduling column; it is **not** a `core.Entry` field —
  it is only passed as a write parameter (see `UpdateExtractState`), mirroring how
  `FeedService` owns `next_check_at` while the store just persists it.

## 4. Core: port, pipeline, driver

Mirrors the existing `Poller` (schedules) / `FeedService.PollFeed` (pipeline, satisfies
`FeedPoller`) split.

### New port `Extractor` (consumer-owned; `internal/extract` implements)

```go
type Extractor interface {
    Extract(ctx context.Context, pageURL string, page []byte) (html string, err error)
}
```

### New interface `EntryScraper` (consumer-owned; like `FeedPoller`)

```go
type EntryScraper interface {
    ScrapeEntry(ctx context.Context, e *Entry) error
}
```

### `ScrapeService` (core; implements `EntryScraper`)

Deps: `EntryStore`, `Fetcher`, `Extractor`, `Sanitizer`, `Clock`, `*slog.Logger`, config.
Pipeline for one pending entry:

1. `Fetcher.Fetch(ctx, FetchRequest{URL: e.URL})` — no ETag/Last-Modified (fresh page fetch).
   **Uses the same `*fetch.Client` instance as feed polling**, so the per-host concurrency
   semaphore is shared → politeness invariant holds; feed polls and article scrapes to one host
   share one budget.
2. Guard: `err`, non-200 status, or `ContentType` not `text/html` → failure path.
3. `Extractor.Extract(ctx, e.URL, resp.Body)` → raw article HTML; `err` or empty → failure.
4. `Sanitizer.Sanitize(html, e.URL)` → safe HTML. **Sanitise-before-persist invariant.**
5. `EntryStore.SetEntryContent(ctx, e.ID, safeHTML)` → sets content, state `done`.

**Failure path** (core owns the scheduling math, unit-tested with a fake `Clock`, like
`PollReschedule`): `attempts = e.ExtractAttempts + 1`; if `attempts >= MaxAttempts` →
`UpdateExtractState(e.ID, ExtractFailed, attempts, nil)` (feed content untouched); else
`next = now + backoff(attempts)` (exponential, capped, + jitter reusing the FeedService jitter
helper) → `UpdateExtractState(e.ID, ExtractPending, attempts, &next)`.

### `Scraper` (driver; mirrors `Poller`)

Deps: `EntryStore` (for `ListPendingExtractions`), `EntryScraper`, `Clock`, logger, config.
`Run(ctx)`:
- ticker every `Tick`;
- each tick: `ListPendingExtractions(now, Batch)` → fan out to a bounded worker pool
  (`Workers`) → `ScrapeEntry` per entry;
- ctx cancellation drains/stops like the poller (close jobs channel, `WaitGroup`).

Freshest-first ordering = **live-over-backfill priority**; the `Batch` per-cycle cap drains a
large backfill over many cycles at the polite shared-host rate.

### New store methods (`EntryStore`)

System-wide (not user-scoped), consistent with poller/cleaner sweeps:

```go
ListPendingExtractions(ctx, now time.Time, limit int) ([]*Entry, error)
    // WHERE extract_state='pending' AND (next_extract_at IS NULL OR next_extract_at<=now)
    // ORDER BY published_at DESC LIMIT ?
SetEntryContent(ctx, entryID ID, content string) error
    // UPDATE content=?, extract_state='done' WHERE id=?
UpdateExtractState(ctx, entryID ID, state ExtractState, attempts int, nextAt *time.Time) error
SetFeedFullContent(ctx, userID, feedID ID, on bool) error   // flips flag (see FeedService below)
```

Backfill/cancel UPDATEs (from `SetFeedFullContent`) may be store methods or inline SQL behind
the service — implementation detail. `UpsertEntries` is unchanged in signature; its insert path
now persists `Entry.ExtractState` + `next_extract_at` (set by `FeedService.ingest`). `MemStore`
in `internal/core/coretest` gains matching **behavioral** impls (honour state filtering +
freshest-first order) so core tests don't pass against a fake that lies.

## 5. Adapter: `internal/extract`

- Wraps `codeberg.org/readeck/go-readability/v2` (MIT, pure Go — keeps `CGO_ENABLED=0`, no cgo).
- `New() *Extractor`; `Extract(ctx, pageURL, page)`: parse `pageURL`, run readability against
  the bytes, return the article HTML (empty → error so the caller treats it as failure).
- **The exact v2 API is confirmed during implementation** — `design.md` notes some `Article`
  values are accessed as methods, not fields, and that this is the maintained replacement for
  the archived `go-shiori/go-readability`.
- Golden-file tested over fixture HTML; the sanitiser is asserted to still strip active content
  from extracted output.

## 6. Wiring & config

### Config (`internal/config`, documented in `design.md` §21)

| Env | Default | Meaning |
|---|---|---|
| `BFEED_SCRAPE_WORKERS` | 20 | extraction worker-pool size |
| `BFEED_SCRAPE_TICK` | 1m | how often the extraction sweep runs |
| `BFEED_SCRAPE_BATCH` | 50 | max entries pulled per cycle (per-cycle cap) |
| `BFEED_SCRAPE_MAX_ATTEMPTS` | 3 | retry cap before terminal `failed` |

Backoff base/cap are internal constants (no env) to limit config sprawl.

### `cmd/bfeed/serve.go`

Build `extract.New()`, `core.NewScrapeService(...)`, `core.NewScraper(...)`; **pass the existing
`fetcher` instance** into the scrape service (shared host budget); start `Scraper.Run` in a
goroutine beside the poller; same ctx-driven shutdown pattern (`pollerDone`-style done channel).

## 7. Web UI

- **Add-feed form**: a "Fetch full content" checkbox. `FeedService.Subscribe` gains a
  `fetchFullContent` option (param or small options struct); it sets `feed.FetchFullContent`
  before `CreateFeed`, so the immediate populate-poll's `ingest` marks the initial batch
  `pending`. Subscribe stays fast — **no inline scraping**.
- **Feed list**: an htmx toggle button per feed → handler → `FeedService.SetFeedFullContent(
  userID, feedID, on)`:
  - flips `feeds.fetch_full_content`;
  - on **enable**: backfill **all** existing entries to `pending` (§3 table);
  - on **disable**: cancel queued (`pending → none`).
  This establishes the first feed-edit affordance (general feed enable/disable UI stays
  deferred per roadmap §A7).
- **Entry view**: a small "full content pending / failed" indicator — **optional**, low
  priority; cut if it adds friction.

## 8. Invariants preserved

- **Sanitise before persistence** (`design.md` §27.1): extracted HTML is sanitised in step 4
  before `SetEntryContent` ever writes it.
- **Politeness** (§27.5): scrape fetches go through the same `Fetcher` and per-host semaphore as
  polls — one shared host budget.
- **Injected Clock** (§27.21): all extraction scheduling/backoff uses `clk.Now()`; the math is
  pure and unit-tested with a fake clock.
- **User scoping** (§27.14): the feed toggle/subscribe paths are user-scoped; the background
  sweep (`ListPendingExtractions`, `SetEntryContent`, `UpdateExtractState`) is a system task by
  design, like the poller.
- **Architecture** (§27.19–20): `Extractor`/`EntryScraper` interfaces declared in core; the
  `extract` adapter depends on core, wired only in `cmd/bfeed`.

## 9. Testing (TDD; stdlib `testing` only; `coretest` fakes; fake Clock)

- **core**: `ScrapeService` happy path (sanitised content written + state `done`); failure
  retries then terminal `failed`; backoff math; `Scraper` dispatch over a batch.
- **store/sqlite** (real temp-file DB): migration applies; `fetch_full_content` round-trips
  through `CreateFeed`/`UpdateFeed`/`feedFromRow`; pending insert; `ListPendingExtractions`
  filter + order asserted via **`EXPLAIN QUERY PLAN`** (partial index used, **no temp B-tree**);
  `SetEntryContent`; backfill/cancel UPDATEs.
- **extract**: golden-file (article HTML → main content); sanitiser strips scripts from output.
- **web**: subscribe-with-checkbox sets the flag; toggle flips + backfills + cancels; user
  scoping enforced.
- Gates before done: `make sqlc` (queries + migrations changed) and commit generated code;
  `make sqlc-check`, `make test-race`, `make lint`.

## 10. Changelog (mandatory)

Add under `[Unreleased] → Added`:
> Opt-in per-feed full-content extraction — bfeed can fetch and extract the full article text
> (Readability) for feeds you flag, replacing the feed-provided snippet; falls back to feed
> content when extraction is disabled or fails.

## 11. Deliberate deviations from `design.md` north star

Recorded here and left noted in `roadmap.md §A3`:

1. **DB-backed extraction state** instead of an in-memory queue + second worker pool
   (`design.md` §13). Durable, restart-safe, simpler; same starvation guarantees via the
   per-cycle batch cap + shared per-host semaphore + freshest-first ordering.
2. **`BFEED_BACKFILL_PER_HOST_PER_CYCLE` dropped** this iteration. The global per-cycle `Batch`
   cap + the shared per-host concurrency semaphore + freshest-first ordering cover the
   "big new feed blocks for ages" / live-over-backfill concern. The per-host-per-cycle cap
   stays **deferred** in the roadmap; add it only if one host's backfill measurably starves
   others.

These remain additive: nothing here rewrites MVP code or data, and the in-memory-pool /
per-host-cap path stays open if ever needed.
