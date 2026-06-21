# bfeed — Bulk mark-all-read (feed page) — design

> Status: approved design for iteration work. Roadmap item A7 "Bulk mark-all-read"
> (`docs/roadmap.md`), design §9.2.
> Priority #1: mark **all** posts of a given feed as read, from the feed page.

## 1. Goal & scope

A reader on the single-feed view (`/feeds/{id}`) can mark **every unread entry in
that feed** read in one click — not just the visible page of 50.

The work is built **reusable**: the selection logic lives in a filter-driven store
method + core service, so home/unread and category views can adopt it later by
passing the filter they already list with. Only the **feed route** is wired in this
iteration.

**Held out (YAGNI):** undo, bulk un-read, a total-unread count in the confirm text,
and wiring the category/home routes (the service + store already support them).

## 2. Decisions (locked)

| Decision | Choice |
|---|---|
| Scope | Feed page now; store + service reusable across views |
| Confirmation | htmx `hx-confirm` native prompt before firing |
| After-action | `HX-Refresh: true` → full page reload (keeps every unread count, row styling, button state honest with zero targeting logic; pages are tiny by design) |

## 3. Store port — one filter-driven method

Add to `EntryStore` (`internal/core/ports.go`):

```go
// MarkReadByFilter marks every UNREAD entry matching the filter's selection
// (FeedID / CategoryID / Uncategorised; an empty selection = all the user's
// feeds) as read, stamping read_at. It ignores pagination/order and the
// Status/Starred/Query fields — it always targets unread. Returns rows affected.
MarkReadByFilter(ctx context.Context, userID ID, f EntryFilter) (int, error)
```

sqlite impl (`internal/store/sqlite/entries.go`) mirrors the dynamic-WHERE pattern
already used by `ListEntries`, minus cursor/order/limit, always forcing
`status='unread'`:

```sql
UPDATE entries SET status = 'read', read_at = ?
WHERE user_id = ? AND status = 'unread'
  [AND feed_id = ?]                                              -- FeedID
  [AND feed_id IN (SELECT id FROM feeds                          -- CategoryID
                   WHERE user_id = ? AND category_id = ?)]
  [AND feed_id IN (SELECT id FROM feeds                          -- Uncategorised
                   WHERE user_id = ? AND category_id IS NULL)]
```

Notes:
- **UPDATE can't JOIN** in SQLite, so category/uncategorised use a `feed_id IN
  (subquery)` instead of `ListEntries`' `JOIN feeds`.
- `read_at` is stamped with `time.Now().UTC().Unix()` **in the store** — identical
  to the existing `SetStatus`. The injected-`Clock` invariant bans `time.Now()` in
  **core**, not in the persistence adapter.
- All values are bound parameters; the WHERE is assembled only from the closed set
  of `EntryFilter` selection fields → no injection surface (same approach as
  `ListEntries`).
- **User scoping** is preserved: `user_id = ?` always present, and the category
  subqueries are themselves user-scoped, so a feed/category id alone can never
  reach across users.
- **Indexes:** the predicate is served by existing `idx_entries_feed_pub`
  (`feed_id, …`) / `idx_entries_user_status_pub` (`user_id, status, …`). There is
  no `ORDER BY`, so no temp B-tree risk and **no new index** is added. This is an
  occasional user click, not a hot path.

`sqlc` is **not** used for this query (hand-written dynamic SQL, like `ListEntries`,
`SetStatus`, `SetStarred`), so no `make sqlc` regeneration is required.

## 4. Core service

`internal/core/entry.go`:

```go
// MarkAllRead marks every unread entry matching f's selection read. Returns the
// number affected.
func (s *EntryService) MarkAllRead(ctx context.Context, userID ID, f EntryFilter) (int, error) {
    return s.store.MarkReadByFilter(ctx, userID, f)
}
```

Thin pass-through — the service owns no extra logic here; the selection contract
lives with the store method's documented semantics.

## 5. Web adapter

- **Route** (`internal/web/web.go`): `POST /feeds/{id}/mark-read` → `h.markFeedRead`
  (placed beside the existing `POST /feeds/{id}/refresh`, `/delete`, `/category`).
- **Handler** (`internal/web/handlers.go`):
  - `parseID`; build `core.EntryFilter{FeedID: &id}`.
  - `n, err := h.entries.MarkAllRead(r.Context(), uid, f)`; on error → 500.
  - Set `w.Header().Set("HX-Refresh", "true")`; `WriteHeader(http.StatusNoContent)`.
    (htmx sees the header and reloads the page; the response body is irrelevant.)
  - Log at debug/info with feed id + `n`.
- **Template** (feed-page list view): a **"Mark all read"** button in the list
  toolbar/header, rendered **only on the feed view**, carrying:
  ```html
  hx-post="/feeds/{{.FeedID}}/mark-read"
  hx-confirm="Mark all entries in this feed as read?"
  ```
  The button always renders on the feed view (a zero-unread click is a harmless
  0-row UPDATE); we do not compute a total-unread count just to hide it.

To know it is the feed view + which feed, the list view-model needs the feed id
available in the template. `feedEntries`/`renderList` already know the path
(`/feeds/{id}`); thread the feed id (or a "mark-read POST path") onto `listVM` so
the toolbar can render the button only when set.

**Reusable next (documented, not built):** `/categories/{id}/mark-read` and a home
`POST /mark-read` call the same `MarkAllRead` with `{CategoryID}` / `{Uncategorised}`
/ empty filter; the button becomes a shared template partial parameterised by POST
path + confirm text.

## 6. Testing (TDD: red → green → refactor)

- **core** (`internal/core`, using `coretest.MemStore`):
  - `MarkAllRead` delegates to the store and returns its count/err.
  - Extend `coretest.MemStore` with `MarkReadByFilter`, honoring the selection
    (FeedID / CategoryID / Uncategorised / empty) and flipping only `unread`
    entries to `read` + setting `ReadAt` (keep the fake behavioral, per the testing
    invariant — it must not lie about filter semantics).
- **store/sqlite** (real temp-file DB):
  - Seed two feeds, two users, mixed read/unread + starred. `MarkReadByFilter{FeedID}`
    marks **only** the target feed's unread entries read, stamps `read_at`, returns
    the right count, and leaves the other feed, the other user, and already-read
    entries untouched. Assert exactly which rows flip.
  - **Starred is not a shield against *read*** (only against TTL deletion): a
    starred-but-unread entry in the target feed **does** flip to read. Assert this.
  - A category-filter case: marks unread across all feeds in the category only.
  - Idempotence: a second call returns 0.
- **web** (`httptest`): `POST /feeds/{id}/mark-read` → service invoked with
  `{FeedID:id}`, response carries `HX-Refresh: true`.

## 7. Changelog (mandatory)

Add under `[Unreleased] / Added`:

> Mark all entries in a feed as read, in one click from the feed page.

## 8. Invariants honored

- User scoping on every query (incl. category subqueries) — design §27.14.
- No `time.Now()` in core; the store stamps `read_at` (as `SetStatus` already does).
- Keyset/index discipline: no `ORDER BY` added, no temp B-tree, no new index.
- Additive: one new port method, one service method, one route, one button — no MVP
  code or data rewritten (roadmap "every item is additive").
