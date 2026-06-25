# Feed manager redesign — design

Status: approved (brainstorm), pending implementation plan
Date: 2026-06-24

## Problem

The `/feeds` page works but is unsatisfying:

- **It blocks.** Subscribe and manual refresh run the full poll pipeline
  synchronously inside the HTTP handler (`subscribe` → `FeedService.Subscribe`;
  `refresh` → `FeedService.Refresh` → `PollFeed`), so the request hangs on a
  network fetch. There is no acknowledgement that the operation started, and a
  slow or dead host stalls the page for seconds.
- **The layout is bulky and flat.** Each feed is a tall card with a row of
  same-weight buttons (category select, full-content toggle, Refresh, Delete).
  The subscribe form sprawls and its "Fetch full content" checkbox clips the
  reading-column edge.
- **It under-informs.** No last-updated or next-scheduled time is shown.
  Errors render as raw text. Counts only refresh on a manual page reload.
- **Editing is missing.** There is no way to rename a feed or change its URL;
  the model has no user-title override column.

## Goals

A single `/feeds` manager that:

1. Adds feeds (category selectable at creation), deletes feeds.
2. Shows per-feed total and unread counts, last-updated and next-scheduled
   times, and any refresh error.
3. Edits a feed's title, URL, category, and full-content setting.
4. Groups feeds by category, with per-group feed and unread counts.
5. Runs refresh and subscribe **in the background** with instant feedback and
   auto-updating counts — no perceived hang, no manual reload.
6. Works well on a phone (iPhone-class viewport).

Non-goals (deferred, see `docs/roadmap.md`): per-feed poll-interval override,
feed enable/disable, OPML import/export, drag-reorder, inline category
creation. Category management stays on the existing `/categories` page.

## Visual direction

The app already has a deliberate identity — Literata serif, IBM Plex Mono,
warm-paper palette, quiet chrome, and an icon "instrument bar" vernacular
(`.actbar` on the entry list and reader). The redesign speaks that same
language; it does not introduce a new look. Boldness stays in one place:
the accent colour appears only on the unread count, the delete-icon hover,
and the active refresh spinner. Everything else is quiet.

A static mock (reusing the real `app.css`) validated the direction on both
desktop (1100px) and iPhone (390px) widths.

### Row anatomy

```
TECH · 3 feeds · 38 unread                                   ← group head (mono, faint)
─────────────────────────────────────────────────────────
Hacker News: Front Page                          ⟳  ✎  🗑   ← serif title (→ entries) + icon bar
hnrss.org · 37 unread / 211 · updated 2h ago · next in 1h    ← single mono meta line
```

- **Title** is a serif link to the feed's entry list (`/feeds/{id}`). Display
  title is `user_title ?? title`.
- **Meta line** is one mono line that wraps on narrow screens:
  `host · <unread> unread / <total> · updated <rel> · next <rel>`.
  The unread count is accent-weighted; the total is faint. `host` is the feed
  URL's host. Relative times use the existing `humanizeSince` helper; "next"
  is derived from `feeds.next_check_at` (omitted when in the past).
- **Icon action bar** (right-aligned, always visible): Refresh (⟳), Edit (✎),
  Delete (🗑). Icons are quiet (`--faint`), darken to `--ink` on hover; delete
  hovers to `--accent`. New inline-SVG `ic-*` defines in `_icons.gohtml`
  (currentColor), reusing the established icon pattern. Tap targets are ≥44px
  on touch (`--tap`) even though the resting glyph is smaller.

### States

- **Refreshing:** meta line shows `refreshing…` (accent), the Refresh icon
  spins (`prefers-reduced-motion` disables the animation), the row polls.
- **Stalled / error:** meta line shows `⚠ stalled — <error>` (red) when
  `error_count >= BFEED_FEED_ERROR_LIMIT`, plus `last ok <rel>`. A single
  recent error (below the limit) shows `error: <message>` without the stalled
  badge. (Matches the existing stalled-badge semantics.)
- **Pending add:** a ghost row titled `Adding <url>…` with `resolving feed…`
  and a ✕ (cancel = delete) in place of the action bar; it polls until the
  feed resolves (fills in) or fails (turns into an error row with ✕ to dismiss).

### Add-feed form

A compact form above the groups: URL input (flex, "paste a feed or site URL"),
category select, a "Full content" checkbox, and a primary **Add feed** button.
On submit it clears instantly and the optimistic pending row appears. The form
wraps to stacked full-width controls on small screens (fixes the current clip).

### Empty state

No feeds → the existing `.empty-state` treatment ("No feeds yet. Add one
above.").

## Background execution

The two slow operations move off the request thread.

### In-flight tracking

A small coordinator in the web layer holds the set of feed ids with an
operation in progress:

```go
type inflight struct {
    mu  sync.Mutex
    ids map[core.ID]struct{}
}
```

It exposes `start(id)`, `done(id)`, and `has(id) bool`. It is a web-layer
concern (single process, single user); core services are unchanged. It exists
only to tell the row fragment whether to keep polling — it is not durable
state, and a process restart simply stops any in-progress spinners (the feed
rows render from the DB, which is the source of truth).

### Refresh

`POST /feeds/{id}/refresh`:

1. `inflight.start(id)`.
2. Spawn a goroutine: `feeds.Refresh(ctx, uid, id)`; on return, `inflight.done(id)`.
   The goroutine uses a background context (`context.Background()` with a
   sensible timeout), **not** the request context, so the refresh survives the
   response returning.
3. Render and return the single-row fragment in its refreshing state (with the
   poll trigger). HTTP 200.

### Subscribe

`POST /feeds`:

1. Validate the URL is well-formed (`http`/`https`) and the category is owned —
   fast, synchronous. On failure, return the inline `subscribeError` fragment
   as today.
2. Create the feed record immediately: `title = feedTitle("", url)` (URL
   fallback), `user_title = NULL`, `category_id`, `fetch_full_content`,
   `next_check_at = now`, `checked_at = NULL`. This is a new
   `FeedService.CreateSubscription` (or a thin store call) that persists the
   row **without** the resolve/ingest the current `Subscribe` does inline.
3. `inflight.start(newID)`; spawn a goroutine that does the real work —
   resolve the feed URL, ingest entries, reschedule (the body of today's
   `Subscribe` minus the up-front create). On failure it records the error on
   the feed (`last_error`, `error_count`) so the pending row becomes an error
   row. On `inflight.done(newID)`.
4. Return the optimistic pending row fragment (with the poll trigger), inserted
   into the correct category group. HTTP 200.

This makes subscribe and refresh behave identically from the UI's point of
view: an immediate row that polls until the background work finishes.

> Refactor note: today's `FeedService.Subscribe` does validate → resolve →
> create → ingest → reschedule in one synchronous call. It splits into
> `CreateSubscription` (validate + create, synchronous) and `ResolveAndIngest`
> (resolve + ingest + reschedule, run in the goroutine). The existing
> `Subscribe` can remain as a convenience that calls both in sequence for
> tests / non-web callers, or be removed if unused.

## Live update (htmx polling)

Chosen over SSE: no persistent connection, no event broker, fully boring for a
single-user app. Latency is one poll interval, which is fine for feed refresh.

### Single-row fragment

New route `GET /feeds/{id}/row` returns the one feed row, rendered fresh from
the DB + current `inflight` state:

- If `inflight.has(id)` → render the refreshing/pending state **with**
  `hx-get="/feeds/{id}/row" hx-trigger="every 1.5s" hx-swap="outerHTML"`.
- Else → render the final row **without** any poll trigger, so htmx stops
  polling automatically.

The refreshing row and the pending/optimistic row are the same fragment in
different states; `subscribe` and `refresh` both return it as their response,
and the poll re-requests it.

### Count propagation

- **Per-feed counts** (unread / total) re-query `EntryStats` each time the row
  fragment renders — they update for free when the poll completes.
- **Group-head counts and the nav unread badge** update via an
  `hx-swap-oob="true"` fragment appended to the completion render (the render
  where `inflight.has(id)` flips to false). The OOB fragment carries the
  updated group head (`#feed-group-{catID}`) and the nav unread badge. This
  keeps aggregates correct without a full page reload and without disturbing
  any open edit panel.

> If the OOB approach proves fiddly during implementation, the fallback is an
> `HX-Trigger` response header firing a `feeds:updated` event that the group
> heads and nav listen for (`hx-trigger="feeds:updated from:body"`). OOB is
> preferred (one round-trip); this is a noted alternative, not a second design.

## Edit

`POST /feeds/{id}` — unified save for the inline edit panel. Form fields:
`title`, `url`, `category_id`, `full_content`. Behaviour:

- `title` → `feeds.user_title` (trimmed; empty clears the override, falling
  back to the poll-owned `title`).
- `category_id` → reuse `FeedService.SetCategory` semantics (ownership check;
  empty = uncategorised).
- `full_content` → reuse `FeedService.SetFullContent` semantics.
- `url` → if changed and well-formed, update `feeds.feed_url` and kick a
  background refresh (re-resolve) exactly like `POST /feeds/{id}/refresh`. The
  feed id is preserved, so existing entries and tombstones persist; the next
  poll updates `etag`/`last_modified`/metadata. A malformed URL rejects the
  whole save with an inline error in the panel.

On success the response swaps the row back to its collapsed (or refreshing, if
the URL changed) state. The separate `POST /feeds/{id}/category` and
`POST /feeds/{id}/full-content` routes fold into this handler and are removed;
their service methods stay (the new handler calls them).

This needs a new `FeedService.SetUserTitle(ctx, uid, id, title string)` and an
`EditFeed` orchestration (or the handler calls the individual setters in
order). Title display resolves `user_title ?? title` in the view model
(`feedTitle`/`singleFeedTitle` and the feeds list row), so the rename surfaces
everywhere a feed title is shown.

## Data model

One additive migration:

```sql
ALTER TABLE feeds ADD COLUMN user_title TEXT;   -- user override; NULL = use poll-owned title
```

- `feeds.title` remains **poll-owned** (refreshed on every successful poll,
  including 304 — invariant unchanged).
- Display title = `COALESCE(user_title, title)`, computed in core/web, never
  written back into `title`.
- sqlc: add `user_title` to the relevant queries / regenerate
  (`make sqlc` + `make sqlc-check`). The `ListEntries`-style dynamic queries are
  unaffected.

No other schema change. Pending / refreshing / error states are expressed with
existing columns (`checked_at IS NULL`, `last_error`, `error_count`) plus the
in-memory `inflight` bit.

## Routes summary

| Method | Path | Change | Behaviour |
|---|---|---|---|
| GET | `/feeds` | redesigned | grouped list, new rows, add form |
| GET | `/feeds/{id}/row` | **new** | single-row status fragment (poll target) |
| POST | `/feeds` | non-blocking | validate + create + optimistic pending row; resolve in goroutine |
| POST | `/feeds/{id}` | **new** | unified edit save (title/url/category/full-content) |
| POST | `/feeds/{id}/refresh` | non-blocking | start background poll, return refreshing row |
| POST | `/feeds/{id}/delete` | unchanged | delete (also the pending/error-row ✕ cancel) |
| POST | `/feeds/{id}/category` | **removed** | folded into `POST /feeds/{id}` |
| POST | `/feeds/{id}/full-content` | **removed** | folded into `POST /feeds/{id}` |

## Web layer (templates)

- `feeds.gohtml` — redesigned `content` block: add form, grouped sections,
  empty state. Group head `#feed-group-{catID}` is OOB-swappable.
- A new feed-row partial (call it `feedrow`) rendered both inline in the list
  and standalone by `GET /feeds/{id}/row` — parsed separately like the existing
  standalone `entryrow` fragment, listing its partials explicitly.
- New `ic-refresh`, `ic-edit` (pencil), `ic-trash`, `ic-x` defines in
  `_icons.gohtml` (currentColor, sized in CSS), reusing the established icon
  convention.
- CSS: a `.feed`/`.feed-meta`/`.feed-acts`/`.feed-edit` block in `app.css`
  following the existing token system; the action icons reuse the `.actbar`
  sizing logic with ≥44px tap targets on touch. The shared `.entry`/`.actions`
  classes used by categories/feeds pages are **not** restyled (per the
  CLAUDE.md warning); the feed manager uses its own `.feed*` classes.

## Concurrency / safety notes

- Background goroutines use a fresh context with a timeout, not the request
  context.
- The store remains single-writer (`SetMaxOpenConns(1)`); concurrent manual
  refreshes serialize at the DB like the poller already does. The `inflight`
  set prevents a second concurrent manual refresh of the same feed from
  double-spawning (start is a no-op / detectable if already present).
- A manual refresh and the background `Poller` touching the same feed is
  already possible today; `PollFeed`'s reschedule is idempotent enough that the
  worst case is a redundant fetch. No new locking introduced.
- No `time.Now()` in core (injected `Clock`); the web layer's relative-time
  rendering keeps using wall-clock as today.

## Testing

- Core: `CreateSubscription` / `ResolveAndIngest` split tested with the
  `coretest` fakes (StubFetcher/StubParser); `SetUserTitle` and the
  `user_title ?? title` resolution; URL-change triggers a refresh path.
- Store (`store/sqlite`): `user_title` round-trips; migration applies; display
  title resolution; hot list queries keep their index plans
  (`EXPLAIN QUERY PLAN`).
- Web: `/feeds/{id}/row` renders refreshing vs final state (poll trigger
  present iff in-flight); subscribe returns an optimistic pending row without
  blocking on resolve (StubFetcher that blocks proves non-blocking via the
  in-flight bit, not wall-clock); edit save updates the four fields and
  redirects/swaps correctly; OOB group/nav count fragment present on
  completion. stdlib `testing` only, fake `Clock`.
- `make test-race` (the background goroutines + `inflight` mutex must be
  race-clean).

## Changelog

User-facing — add under `[Unreleased]`:

- **Added** — rename feeds and edit a feed's URL, category, and full-content
  setting from the redesigned Feeds page.
- **Changed** — adding and refreshing feeds now happen in the background with
  immediate feedback; counts update automatically without a page reload. The
  Feeds page is redesigned: feeds grouped by category with per-feed unread/total
  counts, last-updated and next-update times, and clearer error states.
