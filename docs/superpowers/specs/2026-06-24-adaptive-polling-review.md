# Adaptive feed-poll scheduling — design review

> Status: review feedback on `2026-06-24-adaptive-polling-design.md` (iter 6).
> Adversarial read against the current codebase. Findings are ranked by severity
> and each is backed by a concrete code reference. Not a rejection — the
> mechanism is sound — but several correctness holes should be closed before
> implementation, and the scope is worth trimming.

## Summary

The success/error split (pure `AdaptiveInterval` + unchanged `PollReschedule`)
is the right shape and matches the design.md §12 target. The problem is **what
feeds the adaptive function**: the entire adaptive signal rests on
`entries.published_at`, the least reliable field in real-world RSS, and the
chosen *spacing-based virtual count* is more fragile than the plain `COUNT` the
design rejected.

Three of the four most serious findings (R1–R4) stem from the adaptive count
specifically. The other two deliverables — **TTL honouring** and the
**error-limit badge** — are independent and low-risk.

One fear that turned out **not** to be a problem: `CEIL`/`CAST`/`NULLIF` all
work in the pinned driver (modernc.org/sqlite v1.52.0). Verified directly
against a temp DB — the full `WeeklyEntryCount` expression evaluates correctly.
No SQLite math-function portability issue.

## Tier 1 — correctness (must address)

### R1. Date-less feeds are starved at `MaxInterval` forever

`internal/parse/parse.go:37-42`: if an item has neither `PublishedParsed` nor
`UpdatedParsed`, `pub` is left as the zero `time.Time`. `FeedService.ingest`
(`internal/core/feed.go`) stores `pe.PublishedAt` verbatim — there is **no
fallback to ingest time**. `WeeklyEntryCount`'s window is
`published_at >= now - 604800`, so epoch-0 entries are excluded entirely.

Consequence: a feed that posts 20×/day but omits `<pubDate>` yields
`weeklyCount = 0` → `AdaptiveInterval` returns `MaxInterval` (24h). The busiest
broken feeds get polled the *least*. Date-less and malformed-date feeds are
common; the whole adaptive input trusts the field publishers most often get
wrong.

**Fix:** when `published_at` is absent/zero, fall back to entry creation time
(ingest time) for the frequency calculation. The feed still adapts; only the
display ordering keeps the publisher date.

### R2. No upper window bound → future dates poison the spacing

The query filters `published_at >= now - 604800` but has **no `<= now`**. A
single future-dated entry (timezone bug, publisher posting ahead) inflates
`MAX(published_at) - MIN(published_at)`, shrinks the virtual count, and slows
polling. Miniflux bounds the window on both ends (`BETWEEN now-week AND now`);
this design dropped the upper bound.

**Fix:** add `AND published_at <= ?` (bind `now`).

### R3. Cold-start adapts in the wrong direction

`Subscribe` does one immediate poll. Those entries carry **historical**
`published_at` values, mostly older than a week, so they fall outside the
window → `weeklyCount = 0` → `MaxInterval` (24h). A genuinely high-frequency new
feed therefore waits 24h for its *second* poll, and can never speed up because
it polls too slowly to observe a full window of fresh entries. The feedback loop
runs backwards at the moment it matters most.

**Fix:** seed `NextCheckAt` at `MinInterval` (not the adaptive result) until the
feed has accumulated ≥ N in-window samples.

### R4. The spacing formula relocates burst-pinning; it does not solve it — and it is not "Miniflux-style"

The design rejects plain `COUNT` because "a burst doesn't pin a feed to the
floor." The spacing formula it chose has the opposite failure just as badly.
Worked from the literal SQL:

- **Two entries 1s apart, once a week** → span ≈ 1s, avg spacing ≈ 1s →
  `604800 / 1` ≈ 604800/week → interval ≈ 1s → clamped to `MinInterval`. A
  weekly feed is now polled every 5 minutes, permanently. That *is* floor-pinning
  from a burst.
- **Same-instant dump of 50 entries** → span 0 → `NULLIF(0,0) = NULL` →
  `COALESCE → 0` → `MaxInterval`. A burst is treated as *quiet*.

Verified against the real driver: 3 entries 100s apart yields
`weeklyCount = 6048` → min-pinned. Tiny denominators explode because the formula
has no stability floor on spacing.

Miniflux's actual entry-frequency scheduler is **`COUNT` in a bounded window**
(`7*24*60 / weeklyCount` minutes), not spacing. `COUNT` degrades gracefully:
2 entries/week → ~3.5 days, not 5 minutes. The design's rejected alternative is
the *more* robust one.

**Fix:** use `COUNT(*)` over the `[now-week, now]` window. Simpler SQL (no
`CEIL`/`NULLIF` gymnastics), no exploding denominators, and it's what the design
claims to emulate.

## Tier 2 — design smells (should address)

### R5. The `retryAfter` parameter to `AdaptiveInterval` is dead

`PollFeed` (`internal/core/feed.go:159-179`) routes `429` and `5xx` to
`recordError`; only `200`/`304` reach `recordSuccess` → `AdaptiveInterval`. On
those success paths `FetchResponse.RetryAfter` is always 0. The `retryAfter`
argument is therefore never non-zero in production, and the test in §8
("retryAfter override and may exceed max") exercises an unreachable branch.

**Fix:** drop the parameter, or explicitly document it as success-path-only and
remove the misleading test.

### R6. TTL "may exceed max" is uncapped — one bad tag silences a feed

`sy:updatePeriod=yearly` (frequency 1) computes TTL ≈ 365 days; RSS `<ttl>` is
publisher-supplied minutes and frequently garbage. `maxDur(iv, feedTTL, …)` lets
any of these override `MaxInterval` with no ceiling, so a single malformed hint
polls a feed yearly — with no UI signal that anything is wrong. "Publisher
politeness wins" is reasonable, but it needs a sanity cap (e.g. clamp TTL
influence to ~7–30 days).

### R7. No jitter on the success interval → lockstep herd

The error path jitters (`internal/core/reschedule.go:25`); `AdaptiveInterval` is
fully deterministic. After an OPML import, a batch of feeds first-polled
together with similar cadence will compute identical intervals and re-poll in
lockstep, periodically spiking the per-host concurrency cap.

**Fix:** apply small jitter to the adaptive result, as the error path already
does.

### R8. The hard error-limit is a footgun for marginal gain

Exponential backoff already caps a failing feed at `MaxBackoff` (24h), i.e.
~1 conditional GET per day — already cheap and polite. The hard
dispatch-exclusion saves almost nothing on top of that, but adds a real failure
mode: a feed that returns `5xx` through a multi-week outage is **permanently**
undispatched and (per §4) recoverable only by a manual user refresh. "In
practice the user manually refreshes" is hand-waving for a background system.

**Fix:** either drop the hard exclusion (backoff already bounds the cost) and
keep only the badge, or add a periodic re-probe at `MaxInterval` so excluded
feeds can self-heal.

## Tier 3 — minor

### R9. Double RSS parse on every poll

§5 re-runs the gofeed RSS sub-parser over the bytes on *every* successful RSS
poll, inside the hot worker pool — described as "cheap" with no measurement. The
universal parser already exposes the syndication module via
`feed.Extensions["sy"]`; only `<ttl>` needs the typed parser. Confirm what's in
`Extensions` before committing to a 2× XML parse per poll across every RSS feed.

### R10. MemStore parity reimplements SQLite semantics by hand

§2 asks for a behavioral `WeeklyEntryCount` in `coretest.MemStore` matching "the
same spacing math." Reproducing SQLite integer division, `NULLIF`
NULL-propagation, and `CEIL` rounding in Go is precisely the "fake that lies"
risk CLAUDE.md warns about — divergence will still pass green tests. A `COUNT`
window (R4) is far easier to mirror faithfully and removes most of this risk.

## Scope recommendation

R1–R4 and R7 all trace back to the adaptive count. TTL honouring and the
error-limit badge are independent and safe. Given conditional GET already makes
a 304 cost a few hundred bytes, polling a quiet feed every 15m is already cheap;
the adaptive count buys little for a single-user reader while importing a
cold-start bug, a date-trust bug, and a fragile formula.

Suggested order:

1. **Ship TTL honouring + error badge first** (drop the hard exclude; keep
   backoff). Small, low-risk, immediately useful.
2. **If adaptive is still wanted, do it `COUNT`-based:** count entries in
   `[now-week, now]`, fall back to ingest time when `published_at` is absent,
   seed new feeds at `MinInterval`, and add jitter. Closer to actual Miniflux and
   side-steps R1–R4 and R7.
3. Drop the dead `retryAfter` parameter (R5); cap TTL influence (R6).

## Verified, not a problem

- `CEIL` / `CAST` / `NULLIF` work in modernc.org/sqlite v1.52.0 (tested against a
  temp DB; full `WeeklyEntryCount` expression evaluates). No math-function
  portability concern.
- The `ListDueFeeds` index is fine: `idx_feeds_due ON feeds(next_check_at)
  WHERE disabled = 0` still serves the ordering; the added `error_count < ?`
  becomes a cheap residual row filter, no temp B-tree. §4's instinct to verify
  with `EXPLAIN` is correct; the risk is low.
