# Adaptive feed-poll scheduling — design

> Status: approved design (iter 6, "smarter polling"). Implements the adaptive
> half of roadmap §A2. Source of truth for the long-term target: `docs/design.md` §12.

## Goal

Replace the MVP's fixed poll interval with a Miniflux-style **entry-frequency
adaptive** interval: active feeds are polled more often, quiet feeds less, all
within `[min, max]` bounds, while honouring publisher and server hints. Add a
hard error-limit that excludes hopelessly-broken feeds from dispatch, surfaced
in the UI.

The current MVP polls every feed on a single fixed `BFEED_POLL_INTERVAL` (15m),
rescheduling `now + Interval` on success and exponential backoff on error
(`internal/core/reschedule.go`). This design makes the success interval adaptive
and leaves the error backoff path as-is.

## Scope

**In scope**

1. Adaptive interval (a pure `schedule.go` function) + spacing-based weekly
   virtual entry count (`WeeklyEntryCount` SQL) feeding it.
2. Publisher TTL honouring (`feedTTL` term): parse RSS `<ttl>` / `sy:updatePeriod`
   + `sy:updateFrequency`, persist per feed, honour on every poll (incl. 304).
3. Hard error-limit dispatch exclusion (`BFEED_FEED_ERROR_LIMIT`) + a ⚠ badge on
   the Feeds page for excluded feeds.

**Out of scope (deferred to their own specs, still listed in `docs/roadmap.md` §A2)**

- Per-host token-bucket rate limiter + tighten-on-429/503.
- Robots `Crawl-Delay`.
- Per-feed interval override (min/max pin).

## Approach (decided)

Separate the success-path math from the error-path math:

- **`recordSuccess`** → new pure `AdaptiveInterval(...)` in `internal/core/schedule.go`.
- **`recordError`** → keeps the existing `PollReschedule(...)` exponential backoff
  in `internal/core/reschedule.go`, unchanged.

Two small pure functions, each with one job, each unit-tested with a fake `Clock`.
This matches `docs/design.md` §12 ("pure function in `schedule.go`, unit-tested
with a fake Clock") and keeps each function easy to read and test in isolation.

(Rejected alternatives: extending the single `PollReschedule` to branch between
adaptive and backoff modes — one function, two behaviours, harder to read;
inlining the math in `recordSuccess` — untestable without a full service+store
harness, violates the pure-fn/fake-clock invariant.)

## 1. `schedule.go` — adaptive interval

New file `internal/core/schedule.go`:

```go
const week = 7 * 24 * time.Hour // 604800s

type ScheduleConfig struct {
	MinInterval time.Duration // BFEED_SCHED_MIN_INTERVAL, default 5m
	MaxInterval time.Duration // BFEED_SCHED_MAX_INTERVAL, default 24h
	Factor      float64       // BFEED_SCHED_FACTOR, default 1
}

// AdaptiveInterval returns the poll interval for a successfully-polled feed.
//   weeklyCount = spacing-based virtual entries/week (>= 0; see WeeklyEntryCount)
//   feedTTL     = publisher-declared minimum interval (0 if none)
//   retryAfter  = server Retry-After (0 if none)
func AdaptiveInterval(weeklyCount int, cfg ScheduleConfig, feedTTL, retryAfter time.Duration) time.Duration {
	var iv time.Duration
	if weeklyCount <= 0 {
		iv = cfg.MaxInterval // new/quiet feed
	} else {
		iv = time.Duration(float64(week) / (float64(weeklyCount) * cfg.Factor))
	}
	iv = clamp(iv, cfg.MinInterval, cfg.MaxInterval) // [min, max]
	return maxDur(iv, feedTTL, retryAfter)           // server/publisher hints may exceed max
}
```

`clamp` and `maxDur` are small unexported helpers in the same file.
`recordSuccess` computes `f.NextCheckAt = now.Add(AdaptiveInterval(...))`.

**Why `feedTTL`/`retryAfter` "may exceed max":** an explicit publisher or server
request to slow down always wins over our computed cap — politeness.

## 2. `WeeklyEntryCount` — spacing-based virtual count

Static-shape query → `internal/store/sqlite/queries/entries.sql`, sqlc-generated
(regenerate with `make sqlc`; CI enforces sync):

```sql
-- name: WeeklyEntryCount :one
SELECT COALESCE(CAST(CEIL(
  604800.0 / NULLIF((MAX(published_at) - MIN(published_at)) / NULLIF(COUNT(*) - 1, 0), 0)
) AS INTEGER), 0)
FROM entries
WHERE feed_id = ? AND published_at >= ? - 604800; -- ? = now (unix s)
```

This is a **virtual** count (entries/week implied by the average spacing of the
last week's entries), not `COUNT(*)`, so a burst doesn't pin a feed to the floor:

- A same-instant dump → `MAX(published_at) - MIN(published_at) = 0` →
  `NULLIF(0, 0) = NULL` → division yields `NULL` → `COALESCE(..., 0) = 0` →
  `AdaptiveInterval` returns `MaxInterval`.
- A single entry in the window → `COUNT(*) - 1 = 0` → `NULLIF(0, 0) = NULL` →
  `0` → `MaxInterval`.
- A historical backfill (old `published_at` dates) is mostly excluded by the
  `published_at >= now - 604800` window, so it doesn't inflate the count.

Called only in `recordSuccess` (the only path that needs it). `core.Store` gains
`WeeklyEntryCount(ctx, feedID ID, now time.Time) (int, error)`.
`coretest.MemStore` gets a behavioral implementation (same spacing math over its
in-memory entries) so `core_test` stays honest.

This is a system/feed-scoped aggregate keyed by `feed_id`; like the other
background-sweep queries it takes no `user_id` (the feed id already implies its
owner). It runs once per successful poll, off the hot list path.

## 3. Schema + `Feed` type

- **Migration** (additive): `ALTER TABLE feeds ADD COLUMN ttl_seconds INTEGER;`
  (nullable; `NULL`/absent = no publisher TTL).
- `core.Feed` gains `TTL time.Duration`.
- `store/sqlite` `feeds.go` scan/bind maps `ttl_seconds` ↔ `TTL` via
  `sql.NullInt64` seconds (consistent with `emit_pointers_for_null_types: false`;
  reuse/extend the existing null helpers).
- **TTL is poll-owned** (same invariant as `feeds.title`): refreshed from the
  parsed feed on every successful 2xx poll. On a 304 there is no parse, so the
  persisted `ttl_seconds` is what `AdaptiveInterval` reads — which is exactly why
  it is a column and not a parse-time-only value.

## 4. Error-limit dispatch exclusion

`ListDueFeeds` (sqlc, `queries/feeds.sql`) gains the error-limit predicate and a
parameter:

```sql
-- name: ListDueFeeds :many
SELECT ... FROM feeds
WHERE disabled = 0 AND error_count < ? AND next_check_at <= ?
ORDER BY next_check_at ASC
LIMIT ?;
```

- `core.Store.ListDueFeeds` signature gains `errorLimit int`.
  `Poller.dispatch` passes `cfg.ErrorLimit`.
- This is a **system sweep** (no `user_id`) — the deliberate scoping exception,
  same as today.
- **Relationship to graceful backoff:** backoff (unchanged) already slows a
  failing feed toward `MaxBackoff`; the error-limit is a *hard stop* on top —
  once `error_count >= limit` the feed is not dispatched at all.
- **Recovery:** an excluded feed cannot self-recover (it is never re-polled).
  `error_count` resets on the next *successful* poll, which in practice happens
  when the user manually refreshes/edits the feed (existing behaviour). This is
  the design-intended "surface in the UI rather than silently disable".
- Index: `ListDueFeeds`'s plan must stay temp-B-tree-free; verify with `EXPLAIN
  QUERY PLAN` after the predicate change (the existing `next_check_at` ordering
  index should still serve it; add a covering predicate index only if `EXPLAIN`
  regresses).

## 5. Parser TTL extraction (`internal/parse`)

`core.ParsedFeed` gains `TTL time.Duration`.

gofeed's **universal** `gofeed.Feed` discards RSS `<ttl>` and the syndication
module (`sy:*`), so extraction branches on feed type:

- After the universal parse, when `feed.FeedType` is RSS, run gofeed's RSS
  sub-parser (`github.com/mmcdole/gofeed/rss`) over the same in-memory bytes to
  read `TTL` (minutes), `SyUpdatePeriod` (`hourly|daily|weekly|monthly|yearly`),
  and `SyUpdateFrequency`.
- Effective `TTL = max(<ttl> minutes, updatePeriod / updateFrequency)`.
  Atom and JSON Feed have no standard TTL → `TTL = 0`.
- Implemented as an isolated helper `rssTTL(data []byte) time.Duration`,
  unit-tested against `testdata` fixtures. The second parse is RSS-only and
  cheap (bytes already in memory; no extra fetch).

`recordSuccess` sets `f.TTL = pf.TTL` before calling `AdaptiveInterval`
(poll-owned refresh).

## 6. Config + wiring

`internal/config`: **remove** `PollInterval` (`BFEED_POLL_INTERVAL`). Add:

| Env | Default | Validation |
|---|---|---|
| `BFEED_SCHED_MIN_INTERVAL` | `5m` | `> 0` and `< max` |
| `BFEED_SCHED_MAX_INTERVAL` | `24h` | `> min` |
| `BFEED_SCHED_FACTOR` | `1.0` (float) | `> 0` |
| `BFEED_FEED_ERROR_LIMIT` | `20` | `>= 1` |

Unchanged: `BFEED_POLL_TICK` (`1m`), `BFEED_BATCH_SIZE`, `BFEED_FEED_WORKERS`,
`BFEED_HOST_CONCURRENCY`. The "tick ≪ min interval" invariant holds
(`1m ≪ 5m`); validation may warn if `tick >= min`.

`cmd/bfeed` wiring:

- Build `core.ScheduleConfig{MinInterval, MaxInterval, Factor}` → `FeedService`.
- `FeedService` keeps a `RescheduleConfig` for the **error** path; its `Interval`
  is seeded from `MinInterval` (backoff base), `MaxBackoff` from `MaxInterval`.
- `PollerConfig` gains `ErrorLimit int`, passed to `ListDueFeeds`.
- The web layer learns `errorLimit` (a `web` config field) to render the badge.

This is a **breaking config change** (no back-compat alias, by decision —
pre-1.0, single-user, operator controls the deploy).

## 7. Error-limit UI badge (`internal/web`)

- The Feeds page already renders per-feed stats; extend that viewmodel with a
  `stalled bool` (`error_count >= errorLimit`) and the `last_error` string.
- Render a ⚠ badge next to a stalled feed, `title`/tooltip = `last_error`, so the
  user sees *why* a feed went quiet.
- Reuse existing `.feed`/stats styling; no new top-level CSS class needed beyond
  a small badge rule. Follows the shared-partial conventions in `CLAUDE.md`
  (Feeds/categories pages share `.entry`/`.actions` — do not restyle those for
  this).

## 8. Testing

- **`schedule_test.go`** (pure, fake clock): quiet feed (`count<=0`) → `max`;
  busy feed → clamped to `min`; `factor` scaling (e.g. `2` halves interval);
  `feedTTL` and `retryAfter` override and may exceed `max`; clamp boundaries.
- **store (`store/sqlite`, real temp DB):** `WeeklyEntryCount` spacing cases
  (even cadence, single entry → 0, same-instant burst → 0, empty window → 0);
  `ListDueFeeds` excludes `error_count >= limit` and still includes feeds under
  it; `EXPLAIN QUERY PLAN` shows the expected index, no temp B-tree.
- **`coretest.MemStore`:** behavioral `WeeklyEntryCount` parity; `ListDueFeeds`
  honours `errorLimit`.
- **parse:** `rssTTL` fixtures — RSS `<ttl>`, `sy:updatePeriod`+`updateFrequency`,
  combined (max wins), Atom/JSON → `0`.
- **feed service:** `recordSuccess` sets `NextCheckAt` from `AdaptiveInterval`
  and refreshes `f.TTL`; 304 path reads persisted TTL; `recordError` still backs
  off (regression guard).
- **web:** badge present when `error_count >= limit`, absent below.

## 9. Invariants honoured (per `CLAUDE.md`)

- **Poll-owned metadata:** `ttl_seconds` refreshed on every 2xx (like
  title/site/desc); not a user-editable field.
- **Injected `Clock`:** `schedule.go` is pure over durations; `now` comes from
  `clk.Now()` in the service. No `time.Now()` in core.
- **Sanitise before persistence / keyset pagination / STRICT tables /
  single-writer pool:** untouched.
- **User scoping:** `WeeklyEntryCount` and `ListDueFeeds` are system/feed-scoped
  background sweeps — the documented `user_id`-free exception.
- **sqlc discipline:** `WeeklyEntryCount` and the `ListDueFeeds` change are
  static SQL → `queries/*.sql` + `make sqlc`; no hand-edited generated code.

## 10. Docs / changelog

- **`CHANGELOG.md` `[Unreleased]`** — `Added`: adaptive poll interval (active
  feeds polled more often, quiet feeds less); publisher TTL honouring; broken-feed
  exclusion + Feeds-page warning badge. `Changed`/**breaking**: `BFEED_POLL_INTERVAL`
  removed, replaced by `BFEED_SCHED_MIN_INTERVAL` / `BFEED_SCHED_MAX_INTERVAL` /
  `BFEED_SCHED_FACTOR`; new `BFEED_FEED_ERROR_LIMIT`.
- Update `CLAUDE.md` env list, `docs/mvp-design.md` (note fixed-interval
  superseded), and move the relevant `docs/roadmap.md` §A2 rows to **§ Done
  (iter 6)** on completion.
