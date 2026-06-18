# History view — design

> Status: approved design, ready for implementation planning.
> Iteration: 2 ("Reading polish"). Source backlog item: `roadmap.md` §A7 — "History view
> (recently-read, by `read_at`)". North-star spec: `design.md` §9.2, §8 (EntryFilter `Order`),
> §11 (`idx_entries_readhist`).

## 1. Goal

A read-only list view at `/history` showing entries that have been marked read, ordered by
**when they were marked read** (most-recently-read first). It mirrors the existing `/starred`
view: same row layout, same htmx "Load more" keyset pagination, same per-row actions.

## 2. Scope decisions (settled during brainstorming)

- **Membership.** History = entries with `read_at IS NOT NULL`. (`read_at` is set by
  `SetStatus` exactly when an entry becomes read, and nulled when it becomes unread — so this is
  equivalent to `status = 'read'`, but expressed as `read_at IS NOT NULL` so the query matches
  the partial index below.)
- **Ordering.** `ORDER BY read_at DESC, id DESC`. The `id` tiebreak matters: `read_at` has
  1-second resolution, so same-second ties are common (bulk reads, opening several entries
  quickly) and must break deterministically.
- **Date displayed.** Every row in **every** view (unread, starred, history) shows the entry's
  **published** time as a **relative** string ("2h ago", "3d ago", older → absolute date). This
  is a deliberate, global change to row rendering — it is *not* read-time, and it is *not*
  view-specific. Consequence: there is no view-coupling and no need to scope the date per view;
  the shared row template and single-row htmx fragments stay consistent for free.
- **Un-read interaction.** "Mark unread" from history nulls `read_at`; the row drops out of
  history on next load (identical to how un-starring behaves in `/starred` today). No special
  handling.

## 3. Non-goals (YAGNI)

- No "clear history" or bulk-unread action.
- No TTL-bounding of history (no Cleaner exists in the MVP; history is unbounded for now —
  `design.md` §9.2's "bounded by TTL" is deferred with retention, `roadmap.md` §A8).
- No read-time display anywhere. History sorts by `read_at` but displays relative published
  time like every other view.

## 4. Design

### 4.1 Data — new migration `0002`

Add the partial index that backs the history query. New file
`internal/store/sqlite/migrations/0002_history_index.sql`:

```sql
-- +goose Up
CREATE INDEX idx_entries_readhist ON entries(user_id, read_at DESC, id DESC) WHERE read_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_entries_readhist;
```

**Delta from `design.md` §11:** the design wrote the index as `(user_id, read_at DESC)`. We add
the trailing **`id DESC`** so the keyset `ORDER BY read_at DESC, id DESC` is fully satisfied by
the index with no `USE TEMP B-TREE FOR ORDER BY` — the same shape the existing
`idx_entries_user_status_pub` already uses for the published hot path. `design.md` §11 is updated
to match (see §7).

No schema/table changes; `read_at` and `Entry.ReadAt` already exist.

### 4.2 Core — `Order`, generalized `Cursor`

`internal/core/types.go`:

- Add the order constant:

  ```go
  const (
      OrderPublishedDesc Order = iota // default: newest published first
      OrderReadAtDesc                 // history: most-recently-read first
  )
  ```

- Generalize `Cursor` to a neutral keyset position (approved refactor — Approach A). The wire
  format was already a bare `<unixsecs>:<id>`; only the Go field changes:

  ```go
  // Cursor is the keyset pagination position: the order-column value (unix seconds)
  // and id of the last row returned. The next page selects rows strictly "after" it.
  type Cursor struct {
      Key int64 // unix seconds of the active order column (published_at or read_at)
      ID  ID
  }
  ```

`EntryFilter` is unchanged — `Order` already carries the history selector; no new field.

`internal/core/cursor.go` — encode/decode use `Key` (format unchanged, stays byte-compatible):

```go
func EncodeCursor(c Cursor) string {
    raw := strconv.FormatInt(c.Key, 10) + ":" + strconv.FormatInt(int64(c.ID), 10)
    return base64.RawURLEncoding.EncodeToString([]byte(raw))
}
// DecodeCursor → &Cursor{Key: sec, ID: ID(id)} (nil on malformed)
```

### 4.3 Store — `ListEntries` order switch

`internal/store/sqlite/entries.go`, `ListEntries`: select the order column from `f.Order`.

- `OrderPublishedDesc` (default): column `published_at`. Behavior unchanged.
- `OrderReadAtDesc`: column `read_at`; additionally append the membership predicate
  `read_at IS NOT NULL`. Ordering by `read_at` inseparably implies excluding its NULLs, so this
  predicate is applied whenever this order is set (this is what makes the query match
  `idx_entries_readhist`).

The cursor predicate and `ORDER BY` are built against the chosen column:

```
WHERE ... AND (<col> < ? OR (<col> = ? AND id < ?))   -- when f.Cursor != nil
ORDER BY <col> DESC, id DESC
LIMIT ?                                                -- limit+1, same as today
```

The next-page cursor is built from the boundary row's order column via a small helper, e.g.
`sortKey(e *core.Entry, ord core.Order) int64` returning `e.PublishedAt.Unix()` or
`e.ReadAt.Unix()` (non-nil guaranteed by the membership predicate). `next = &core.Cursor{Key:
sortKey(last, f.Order), ID: last.ID}`.

`<col>` is chosen from a closed switch on `f.Order` (never interpolated from user input) — no SQL
injection surface; the dynamic-WHERE pattern already in `ListEntries` is preserved.

### 4.4 Web

`internal/web/web.go`: register `mux.HandleFunc("GET /history", h.history)`.

`internal/web/handlers.go`:

```go
func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
    h.renderList(w, r, "History", "/history", core.EntryFilter{Order: core.OrderReadAtDesc})
}
```

`renderList` is unchanged — it already decodes the incoming cursor, sets `Limit`, and encodes
`next` generically via `core.EncodeCursor`, which now round-trips `Key`.

**Relative date (global).** Add a pure helper and route `entryVM.Published` through it:

```go
// humanizeSince renders the gap between t and now as a short relative string,
// falling back to an absolute date for anything older than ~30 days.
func humanizeSince(t, now time.Time) string {
    d := now.Sub(t)
    switch {
    case d < time.Minute:        return "just now"
    case d < time.Hour:          return fmt.Sprintf("%dm ago", int(d.Minutes()))
    case d < 24*time.Hour:       return fmt.Sprintf("%dh ago", int(d.Hours()))
    case d < 30*24*time.Hour:    return fmt.Sprintf("%dd ago", int(d.Hours()/24))
    default:                     return t.Format("2006-01-02")
    }
}
```

`toEntryVM` changes its one line from `e.PublishedAt.Format("2006-01-02 15:04")` to
`humanizeSince(e.PublishedAt, time.Now())`. This applies to all views (unread, starred, history,
single feed) because they all flow through `toEntryVM`. Future timestamps (`d < 0`) fall into
`"just now"`, which is acceptable.

`internal/web/templates/layout.gohtml`: add a `History` link to the `<nav>` next to
`Unread / Feeds / Starred`.

No changes to `entryVM` fields, the `entryrow`/`entrylist` templates, or the toggle-fragment
handlers — they keep showing `.Published`, which is now relative everywhere.

**Clock note.** `humanizeSince` is a pure function tested with an injected `now`; only the thin
presentation call site uses `time.Now()`. Invariant 21 ("core uses the injected `Clock`") is
scoped to core; the web adapter is a driving adapter and may read wall-clock for presentation,
the same deliberate exception the `store/sqlite` adapter already makes for `read_at`. (If we
later want handler-level determinism, a `core.Clock` can be injected into `web.Handler` via
`New(...)` — out of scope here.)

### 4.5 Test doubles — `coretest.MemStore` (currently lies)

Two fixes so core/web tests can exercise history faithfully:

- `SetStatus`: set `e.ReadAt` to a non-nil time when marking read, and `nil` when marking
  unread — mirroring the real adapter. (Use `time.Now().UTC()`; tests that need exact ordering
  seed `ReadAt` directly on entries.)
- `ListEntries`: honor `f.Order` — for `OrderReadAtDesc`, filter to `ReadAt != nil` and sort
  `read_at` desc then `id` desc; for `OrderPublishedDesc`, sort `published_at` desc then `id`
  desc. Honor `f.Cursor` (skip rows not strictly after `(Key, ID)`) and `f.Limit` (return
  `limit+1` worth, emit `next`). This closes the existing gap where the fake ignored `Cursor`
  and `Order` entirely.

## 5. Testing (TDD: red/green/refactor)

- **`store/sqlite` (authoritative):**
  - `TestHistoryOrderAndKeyset`: insert entries, set `read_at` to known values via raw `UPDATE`
    (because `SetStatus` uses wall-clock and can't be controlled), then `ListEntries` with
    `Order: OrderReadAtDesc` asserts: unread (`read_at IS NULL`) entries excluded; order is
    `read_at` desc with `id` desc tiebreak on equal seconds; keyset pagination walks pages
    correctly with no duplicates/gaps.
  - `TestHistoryUsesIndex`: `EXPLAIN QUERY PLAN` for
    `SELECT id FROM entries WHERE user_id=1 AND read_at IS NOT NULL ORDER BY read_at DESC, id DESC LIMIT 50`
    asserts `idx_entries_readhist` is used and the plan contains no `USE TEMP B-TREE FOR ORDER BY`
    (mirrors the existing `TestHotListUsesIndex`).
- **`core/cursor_test`:** round-trip `Cursor{Key, ID}` through `EncodeCursor`/`DecodeCursor`;
  malformed input → nil.
- **`web`:**
  - `humanizeSince` table test across each threshold boundary (just now / m / h / d / absolute),
    including a future timestamp.
  - `/history` handler lists only read entries, most-recently-read first; "Load more" follows the
    cursor; rows render the relative published string.
- **Adjust existing tests** that reference `Cursor.PublishedAt` to use `Cursor.Key` (the cursor
  round-trip test and any core list/pagination test).

## 6. Invariants preserved (`design.md` §27)

- **#14 user scoping** — history query keeps `user_id = ?` first; `DefaultUserID` in the MVP.
- **#18 keyset, never OFFSET** — history paginates by `(read_at, id)` keyset.
- **#17 persistence shape** — no table changes; new index only; STRICT/epoch/0-1 untouched.
- **Architecture #19/#20** — change stays within existing layers; core owns `Order`/`Cursor`;
  the store and web adapters depend on core, not vice-versa.
- **#21 Clock** — core logic unchanged; only the web presentation layer reads wall-clock, via a
  pure, separately-tested helper (documented exception, §4.4).

## 7. Documentation updates (part of this change)

- `design.md` §11: change `idx_entries_readhist` definition to
  `(user_id, read_at DESC, id DESC) WHERE read_at IS NOT NULL`; add a decision-log line noting
  the `id DESC` tiebreak and the global relative-time row rendering.
- `roadmap.md`: move §A7 "History view" row to **§ Done — done (iter 2)**; note in §B1 that
  `idx_entries_readhist` shipped.
- `mvp-design.md`: remove History view from the deferred "Reading extras" row (line ~37) and drop
  `idx_entries_readhist` from the deferred-additive-index list (line ~301); note `/history` as a
  shipped view alongside `/starred`.

## 8. Implementation order (for the plan)

1. Migration `0002` + `TestHistoryUsesIndex` (red → green).
2. Core: `OrderReadAtDesc`, `Cursor.Key` refactor, `cursor.go`, fix dependent tests.
3. Store: `ListEntries` order switch + `TestHistoryOrderAndKeyset`.
4. `MemStore` fixes.
5. Web: `humanizeSince` (+ test), `toEntryVM` relative date, `/history` route + handler + nav,
   handler test.
6. Doc updates.
7. `go build` (CGO_ENABLED=0), `go test ./... -race`, `go vet`, `gofmt -l`.
