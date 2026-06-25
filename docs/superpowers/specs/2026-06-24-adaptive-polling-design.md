# Adaptive feed-poll scheduling — design

> Status: approved design (iter 6, "smarter polling"), revised after the
> adversarial review in `2026-06-24-adaptive-polling-review.md`. Implements the
> adaptive half of roadmap §A2. Long-term target: `docs/design.md` §12 (this
> design **amends** §12 — see §11).

## Goal

Replace the MVP's fixed poll interval with an entry-frequency **adaptive**
interval: active feeds polled more often, quiet feeds less, within `[min, max]`
bounds, honouring publisher (`feedTTL`) and server hints. Surface persistently
failing feeds with a Feeds-page warning badge.

The current MVP polls every feed on a fixed `BFEED_POLL_INTERVAL` (15m),
rescheduling `now + Interval` on success and exponential backoff on error
(`internal/core/reschedule.go`). This design makes the **success** interval
adaptive; the **error** backoff path is unchanged.

## Scope

**In scope**

1. Adaptive interval (pure `schedule.go` fn) fed by a **`COUNT`-in-window** weekly
   entry count.
2. Publisher TTL honouring (`feedTTL`): RSS `<ttl>` / `sy:updatePeriod` +
   `sy:updateFrequency`, persisted per feed, honoured on every poll (incl. 304),
   capped so a bad tag can't silence a feed.
3. A ⚠ Feeds-page badge for feeds whose `error_count` has reached a threshold.

**Out of scope** (deferred, still in `docs/roadmap.md` §A2)

- Per-host token-bucket rate limiter + tighten-on-429/503.
- Robots `Crawl-Delay`.
- Per-feed interval override (min/max pin).
- **Hard error-limit dispatch exclusion** — dropped (see §4 / review R8):
  exponential backoff already caps a failing feed at ~1 conditional GET/day, so
  a hard exclude saves almost nothing while risking a feed permanently stuck
  undispatched through a transient outage. We keep the badge, not the exclusion.

## Approach (decided)

Separate success-path math from error-path math:

- **`recordSuccess`** → new pure `AdaptiveInterval(...)` in `internal/core/schedule.go`.
- **`recordError`** → existing `PollReschedule(...)` exponential backoff
  (`internal/core/reschedule.go`), unchanged.

Two small pure functions, each unit-tested with a fake `Clock`. Matches
`docs/design.md` §12's "pure function in `schedule.go`".

## 1. `schedule.go` — adaptive interval

New file `internal/core/schedule.go`:

```go
const week = 7 * 24 * time.Hour            // 604800s
const maxTTLInfluence = 30 * 24 * time.Hour // R6: a publisher hint can slow, but not silence

type ScheduleConfig struct {
	MinInterval time.Duration // BFEED_SCHED_MIN_INTERVAL, default 5m
	MaxInterval time.Duration // BFEED_SCHED_MAX_INTERVAL, default 24h
	Factor      float64       // BFEED_SCHED_FACTOR, default 1
}

// AdaptiveInterval returns the poll interval for a successfully-polled feed.
//   weeklyCount = entries in the last week (COUNT; see WeeklyEntryCount), >= 0
//   feedTTL     = publisher-declared minimum interval (0 if none)
//   jitter      = small +/- spread (nil in tests for determinism)
func AdaptiveInterval(weeklyCount int, cfg ScheduleConfig, feedTTL time.Duration,
	jitter func(time.Duration) time.Duration) time.Duration {

	var iv time.Duration
	if weeklyCount <= 0 {
		iv = cfg.MaxInterval // quiet feed
	} else {
		iv = time.Duration(float64(week) / (float64(weeklyCount) * cfg.Factor))
	}
	iv = clamp(iv, cfg.MinInterval, cfg.MaxInterval) // [min, max]

	if feedTTL > 0 { // R6: honour publisher politeness, but capped
		iv = maxDur(iv, minDur(feedTTL, maxTTLInfluence))
	}
	if jitter != nil { // R7: avoid a lockstep herd (the error path already jitters)
		iv += jitter(iv)
	}
	return iv
}
```

`clamp`, `maxDur`, `minDur` are small unexported helpers in the file.

**Cold start (R3) — handled in `recordSuccess`, not the pure fn.** A feed younger
than one week has too few observed samples to trust the count (a high-frequency
new feed's first poll carries *historical* `published_at` dates, all outside the
window → count 0 → would wrongly pick `MaxInterval`). So:

```
if now.Sub(f.CreatedAt) < week:
    f.NextCheckAt = now + MinInterval(+jitter)   // observe before adapting
else:
    f.NextCheckAt = now + AdaptiveInterval(weeklyCount, cfg, f.TTL, jitter)
```

Conditional GET keeps the first week of `MinInterval` polling cheap (a 304 is a
few hundred bytes). After a week of observation the feed adapts normally; a
genuinely quiet established feed then settles to `MaxInterval`.

**Why `retryAfter` is not a parameter (R5):** only `200`/`304` reach
`recordSuccess`; `429`/`5xx` route to `recordError`. `FetchResponse.RetryAfter`
is always 0 on the success path, so the parameter would be dead. `Retry-After`
is honoured where it actually occurs — the error path's `PollReschedule`.

## 2. `WeeklyEntryCount` — COUNT in a bounded window

Static-shape query → `internal/store/sqlite/queries/entries.sql`, sqlc-generated
(`make sqlc`; CI enforces sync):

```sql
-- name: WeeklyEntryCount :one
SELECT COUNT(*) FROM entries
WHERE feed_id = ?
  AND (CASE WHEN published_at > 0 THEN published_at ELSE created_at END)
      BETWEEN ? AND ?;   -- ?2 = now - 604800, ?3 = now (unix s)
```

Decisions baked in:

- **`COUNT`, not the spacing formula (R4).** A plain count over a bounded window
  degrades gracefully (2 entries/week → `week/2` ≈ 3.5d) and has no
  tiny-denominator explosion. The historical-dump burst the spacing formula was
  meant to resist is *already* excluded by the window. The rejected-in-v1
  alternative is the more robust one.
- **Both window bounds (R2).** `BETWEEN now-week AND now` — a future-dated entry
  (timezone bug, publisher posting ahead) cannot enter the window.
- **Ingest-time fallback (R1).** `published_at` is `NOT NULL` and stored `0` for
  date-less items. `CASE WHEN published_at > 0 THEN published_at ELSE created_at`
  uses the entry's ingest time (`created_at`, always set in `ingest`) for the
  frequency signal when the publisher omits a date. Display ordering still uses
  `published_at` — only the frequency estimate gains the fallback. This stops
  busy-but-date-less feeds (common) from being starved at `MaxInterval`.

`core.Store` gains `WeeklyEntryCount(ctx, feedID ID, now time.Time) (int, error)`,
called only in `recordSuccess`. `coretest.MemStore` mirrors it faithfully — a
`COUNT` over in-memory entries with the same effective-date/window rule is trivial
to reproduce (R10: no SQLite `NULLIF`/`CEIL` semantics to fake).

System/feed-scoped aggregate keyed by `feed_id` → no `user_id` (documented
background-sweep exception). Off the hot list path (once per successful poll).
The `CASE` expression can't fully use a published_at index, but `idx_entries_feed_pub
(feed_id, published_at DESC)` still narrows to one feed's rows; verify the plan
with `EXPLAIN QUERY PLAN` and add a covering index only if it regresses.

## 3. Schema + `Feed` type

- **Migration** (additive): `ALTER TABLE feeds ADD COLUMN ttl_seconds INTEGER;`
  (nullable; `NULL` = no publisher TTL).
- `core.Feed` gains `TTL time.Duration`.
- `store/sqlite` `feeds.go` scan/bind maps `ttl_seconds` ↔ `TTL` via
  `sql.NullInt64` seconds (consistent with `emit_pointers_for_null_types: false`).
- **TTL is poll-owned** (same invariant as `feeds.title`): refreshed from the
  parsed feed on every successful 2xx poll; read from the column on a 304 (no
  parse) — which is why it is persisted, not parse-time-only.

## 4. Error badge (no dispatch change)

The hard error-limit dispatch exclusion is **dropped** (review R8). `ListDueFeeds`
is **unchanged** — backoff already bounds a failing feed's cost, and a hard
exclude risks a feed stuck permanently undispatched after a transient outage
(manual-refresh-only recovery is hand-waving for a background system). Backoff
self-heals on the next success.

What remains is purely presentational:

- `BFEED_FEED_ERROR_LIMIT` (default 20) becomes a **display threshold** only — it
  does not touch dispatch. A feed is "stalled" when `error_count >= limit`.
- The web layer learns this threshold (a `web` config field) to render the badge.

## 5. Parser TTL extraction (`internal/parse`)

`core.ParsedFeed` gains `TTL time.Duration`. **No blanket double-parse (R9).**
The universal `gofeed.Feed` (v1.3.0) exposes `Extensions`, `Custom`, and
`FeedType`:

- `sy:updatePeriod` / `sy:updateFrequency` are a namespaced module → read from
  `feed.Extensions["sy"]` directly (no second parse).
- RSS `<ttl>` (un-namespaced core element) → check `feed.Custom["ttl"]` first.
  Only if gofeed's universal translator does not surface `<ttl>` there, fall back
  to a single RSS-typed parse (`gofeed/rss`) of the same in-memory bytes — and
  only for `FeedType == "rss"`. Confirm `Custom`/`Extensions` contents against a
  fixture at implementation time before adding any second parse.
- Effective `TTL = max(<ttl> minutes, updatePeriod / updateFrequency)`. Atom /
  JSON Feed have no standard TTL → `0`.
- Isolated helper `feedTTL(feed *gofeed.Feed, data []byte) time.Duration`,
  unit-tested against `testdata` fixtures.

`recordSuccess` sets `f.TTL = pf.TTL` (poll-owned) before computing the interval.

## 6. Config + wiring

`internal/config`: **remove** `PollInterval` (`BFEED_POLL_INTERVAL`). Add:

| Env | Default | Validation |
|---|---|---|
| `BFEED_SCHED_MIN_INTERVAL` | `5m` | `> 0` and `< max` |
| `BFEED_SCHED_MAX_INTERVAL` | `24h` | `> min` |
| `BFEED_SCHED_FACTOR` | `1.0` (float) | `> 0` |
| `BFEED_FEED_ERROR_LIMIT` | `20` | `>= 1` (badge threshold only — §4) |

Unchanged: `BFEED_POLL_TICK` (`1m`), `BFEED_BATCH_SIZE`, `BFEED_FEED_WORKERS`,
`BFEED_HOST_CONCURRENCY`. The "tick ≪ min interval" invariant holds
(`1m ≪ 5m`); validation warns if `tick >= min`.

`cmd/bfeed` wiring:

- `core.ScheduleConfig{MinInterval, MaxInterval, Factor}` → `FeedService`.
- `FeedService` keeps a `RescheduleConfig` for the **error** path (`Interval`
  seeded from `MinInterval`, `MaxBackoff` from `MaxInterval`) and reuses its
  existing `Jitter` func for the success interval too (R7).
- The web layer learns `errorLimit` for the badge (§7).

**Breaking config change** (no back-compat alias — pre-1.0, single-user).

## 7. Error badge (`internal/web`)

- Feeds page already renders per-feed stats; extend that viewmodel with
  `stalled bool` (`error_count >= errorLimit`) + the `last_error` string.
- Render a ⚠ badge next to a stalled feed, `title`/tooltip = `last_error`.
- Reuse existing `.feed`/stats styling plus a small badge rule; do not restyle the
  shared `.entry`/`.actions` classes (per `CLAUDE.md`).

## 8. Testing

- **`schedule_test.go`** (pure, `jitter=nil`): quiet feed (`count<=0`) → `max`;
  busy feed → clamped to `min`; `factor` scaling (`2` halves); `feedTTL` raises
  interval but is capped at `maxTTLInfluence` (R6); clamp boundaries. A separate
  case asserts a non-nil jitter perturbs the result (R7).
- **feed service** (fake clock + `MemStore`): cold start — feed age `< week` →
  `MinInterval` regardless of count (R3); aged feed adapts from the count; 304
  reads persisted `f.TTL`; date-less entries still produce a non-zero count via
  the ingest-time fallback (R1); `recordError` still backs off (regression guard).
- **store (`store/sqlite`, real temp DB):** `WeeklyEntryCount` cases — even
  cadence; future-dated entry excluded (R2); date-less entry counted via
  `created_at` (R1); empty window → 0; `EXPLAIN QUERY PLAN` acceptable.
- **`coretest.MemStore`:** behavioral `WeeklyEntryCount` parity (R10).
- **parse:** `feedTTL` fixtures — RSS `<ttl>`, `sy:updatePeriod`+`updateFrequency`,
  combined (max wins), Atom/JSON → `0`; assert no second parse when `Extensions`
  /`Custom` already carry the values (R9).
- **web:** badge present when `error_count >= limit`, absent below; `ListDueFeeds`
  unchanged (no dispatch regression).

## 9. Invariants honoured (per `CLAUDE.md`)

- **Poll-owned metadata:** `ttl_seconds` refreshed on every 2xx (like title); not
  user-editable.
- **Injected `Clock`:** `schedule.go` is pure over durations; `now` from
  `clk.Now()` in the service. No `time.Now()` in core.
- **Sanitise before persistence / keyset pagination / STRICT / single-writer:**
  untouched.
- **User scoping:** `WeeklyEntryCount` is a system/feed-scoped sweep — the
  documented `user_id`-free exception. `ListDueFeeds` unchanged.
- **sqlc discipline:** `WeeklyEntryCount` is static SQL → `queries/*.sql` +
  `make sqlc`; no hand-edited generated code.

## 10. Docs / changelog

- **`CHANGELOG.md` `[Unreleased]`** — `Added`: adaptive poll interval (active
  feeds polled more often, quiet feeds less); publisher TTL honouring; Feeds-page
  warning badge for persistently failing feeds. `Changed`/**breaking**:
  `BFEED_POLL_INTERVAL` removed → `BFEED_SCHED_MIN_INTERVAL` /
  `BFEED_SCHED_MAX_INTERVAL` / `BFEED_SCHED_FACTOR`; new `BFEED_FEED_ERROR_LIMIT`.
- Update `CLAUDE.md` env list, `docs/mvp-design.md` (fixed-interval superseded),
  amend `docs/design.md` §12 (§11 below), and move the relevant
  `docs/roadmap.md` §A2 rows to **§ Done (iter 6)** on completion.

## 11. Amendment to `docs/design.md` §12

§12 specifies a *spacing-based virtual count* for `weeklyCount`. The review
verified that formula min-pins on tiny denominators (e.g. two entries 1s apart →
`MinInterval` forever) and treats a same-instant dump as quiet. This design
supersedes it with a **`COUNT` over `[now-week, now]` with an ingest-time
fallback** (§2). `docs/design.md` §12 will be amended to match (replace the
`WeeklyEntryCount` SQL and the "spacing-based" rationale; keep the rest of §12 —
the success/error split, clamp, TTL/Retry-After honouring, graceful backoff).

## 12. Review resolutions (`2026-06-24-adaptive-polling-review.md`)

| # | Verdict | Resolution |
|---|---|---|
| R1 date-less starvation | accepted | ingest-time fallback in `WeeklyEntryCount` (§2) |
| R2 no upper window bound | accepted | `BETWEEN now-week AND now` (§2) |
| R3 cold-start backwards | accepted | seed `MinInterval` while feed age `< week` (§1) |
| R4 spacing fragile | accepted | switched to `COUNT`-in-window (§2, §11) |
| R5 dead `retryAfter` param | accepted | dropped from `AdaptiveInterval` (§1) |
| R6 uncapped TTL | accepted | `maxTTLInfluence` 30d cap (§1) |
| R7 no jitter | accepted | jitter on success interval (§1) |
| R8 hard error-limit footgun | accepted | dropped exclusion; badge only (§4) |
| R9 double parse | accepted | prefer `Extensions`/`Custom`; typed parse only if needed (§5) |
| R10 MemStore parity lies | accepted | `COUNT` is faithfully mirrorable (§2) |
| CEIL/NULLIF portability | n/a | moot — no longer used (COUNT) |
| `ListDueFeeds` index | n/a | unchanged (exclusion dropped) |
