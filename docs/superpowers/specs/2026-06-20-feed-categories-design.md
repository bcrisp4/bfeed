# bfeed — Feed Categories Design (iteration 3)

> Status: approved design, ready for an implementation plan.
> Scope: the **categories** half of roadmap iter 3 (`roadmap.md` A6). FTS5 search — the other
> half of iter 3 — is deliberately out of scope and gets its own spec.
> Authoritative specs this builds on: [`design.md`](../../design.md) §7, §9.1, §11, §18, §27;
> [`mvp-design.md`](../../mvp-design.md); tracked in [`roadmap.md`](../../roadmap.md) A6.

---

## 1. Goal & scope

Let the single user organise feeds into **categories** and read an **aggregated entry stream**
per category (all entries across the category's feeds), the way Miniflux does. A feed belongs to
at most one category; unassigned feeds are **uncategorised**.

This is **purely additive**, exactly as `roadmap.md` promises: one new migration, new core types
/ports/service, new store queries, new web routes/templates. **No MVP table, query, or code path
is rewritten**, and existing data is untouched. The app stays single-user (`core.DefaultUserID`
= 1); category rows still carry `user_id` so multi-user remains a later additive change.

### In scope
- A `categories` table and a nullable `feeds.category_id` (nil = uncategorised).
- Category CRUD (create, rename, delete) with delete re-homing feeds to uncategorised.
- Assigning a feed's category at **subscribe time** and **changing it afterwards**.
- A `/categories` index (categories + unread counts, including an uncategorised bucket) and an
  aggregated **category entry stream** (`/categories/{id}`, plus `/categories/none` for the
  uncategorised bucket), keyset-paginated like every other list.
- The `/feeds` page regrouped under category headings, with a per-feed category control.

### Out of scope (stays deferred; tracked in `roadmap.md`)
FTS5 search; OPML category import/export mapping; multi-user; nested/hierarchical categories;
category reordering / drag-and-drop; per-category "mark all read".

---

## 2. The one real architectural choice — entry→category querying

Entries denormalise `user_id` but **not** category; category lives on `feeds.category_id`. The
aggregated stream needs "all entries whose feed is in category X, newest-first, keyset-paginated".

**Decision: JOIN `feeds` in `ListEntries`.**

```sql
SELECT e.<cols>
FROM entries e JOIN feeds f ON e.feed_id = f.id
WHERE e.user_id = ? AND f.category_id = ?      -- (or f.category_id IS NULL for uncategorised)
  [AND e.status = ? ...]                        -- existing optional filters
ORDER BY e.published_at DESC, e.id DESC
LIMIT ?;
```

The category stream is **all-statuses** (§7.4), so `status` is not pinned by equality. The existing
`idx_entries_user_status_pub (user_id, status, published_at DESC, id DESC)` only yields
published-order when `status` is constrained, so it cannot serve this query sort-free. This
iteration therefore adds **`idx_entries_user_pub (user_id, published_at DESC, id DESC)`**: the
planner walks it in published order for the user and probes `feeds` by integer-PK rowid to test
`category_id` — so **ordering needs no temp B-tree and keyset pagination is preserved**. The new
index is also a generally useful user-wide chronological index and is not redundant with the
status-prefixed one.

Rejected alternatives:
- **`feed_id IN (SELECT id FROM feeds WHERE category_id = ?)`** — merging several `feed_id` groups
  under one global `published_at` order risks `USE TEMP B-TREE FOR ORDER BY`, breaking the keyset
  invariant (`design.md` §27.18).
- **Denormalise `category_id` onto `entries`** (mirroring `user_id`) — moving a feed between
  categories would require bulk-rewriting all its entries. Not worth it.

The chosen plan is verified by an `EXPLAIN QUERY PLAN` test, per the existing `store/sqlite`
convention (hot list queries assert index use + no temp B-tree). If a temp B-tree ever appears,
the fallback is a covering index, not a query rewrite.

---

## 3. Domain types (`internal/core/types.go`)

```go
type Category struct {
    ID     ID
    UserID ID
    Title  string
}
```

- `Feed` gains `CategoryID *ID` // nil = uncategorised.
- `EntryFilter` gains:
  - `CategoryID *ID`        // filter to one category (via the §2 JOIN)
  - `Uncategorised bool`    // the null bucket; distinct from a nil CategoryID, which still means "all"

`CategoryID` and `Uncategorised` are mutually exclusive in practice; the web layer sets exactly one.

---

## 4. Ports (`internal/core/ports.go`)

New consumer-owned `CategoryStore`, composed into `Store` (matches `design.md` §8):

```go
type CategoryStore interface {
    CreateCategory(ctx context.Context, c *Category) (ID, error)            // UNIQUE(user_id,title) → ErrConflict
    GetCategory(ctx context.Context, userID, id ID) (*Category, error)
    ListCategories(ctx context.Context, userID ID) ([]*Category, error)     // title COLLATE NOCASE ASC
    UpdateCategory(ctx context.Context, c *Category) error                  // rename
    DeleteCategory(ctx context.Context, userID, id ID) error                // feeds → uncategorised (ON DELETE SET NULL)
    UnreadCountsByCategory(ctx context.Context, userID ID) (perCat map[ID]int, uncategorised int, err error)
}

type Store interface {
    FeedStore
    EntryStore
    CategoryStore
}
```

`FeedStore` gains one method:

```go
SetFeedCategory(ctx context.Context, userID, feedID ID, categoryID *ID) error  // nil clears (uncategorised)
```

A focused `SetFeedCategory` (rather than overloading `UpdateFeed`, which the poller owns) keeps
re-assignment a single small write and avoids the web layer round-tripping the whole feed row.

---

## 5. Services

### 5.1 `CategoryService` (new `internal/core/category.go`)
Plain struct, constructor DI, `*slog.Logger` — same shape as the other services.
- `Create(ctx, userID, title)` — trim; empty → `ErrValidation`; delegates to store (unique → `ErrConflict`).
- `List(ctx, userID)` — pass-through.
- `Get(ctx, userID, id)` — pass-through.
- `Rename(ctx, userID, id, title)` — trim/validate; store `UpdateCategory`.
- `Delete(ctx, userID, id)` — store `DeleteCategory` (feeds re-home via FK `SET NULL`).
- `UnreadCounts(ctx, userID)` — pass-through to `UnreadCountsByCategory`.

### 5.2 `FeedService` (`internal/core/feed.go`)
- `Subscribe` gains a trailing `categoryID *ID` param. When non-nil, validate ownership with
  `store.GetCategory` (returns `ErrValidation`/`ErrNotFound` on a bad/foreign id) **before**
  `CreateFeed`, and set `f.CategoryID`. (Minor additive signature change; updates the web
  subscribe handler and feed tests.)
- New `SetCategory(ctx, userID, feedID ID, categoryID *ID) error` — same ownership check when
  non-nil, then `store.SetFeedCategory`.

### 5.3 `EntryService` (`internal/core/entry.go`)
Unchanged. `List` already forwards the whole `EntryFilter`; the new fields flow straight through.

---

## 6. Store / SQL (`internal/store/sqlite`)

### 6.1 Migration `migrations/0003_categories.sql` (goose up/down)
```sql
-- +goose Up
CREATE TABLE categories (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  UNIQUE (user_id, title)
) STRICT;
CREATE INDEX idx_categories_user ON categories(user_id);

ALTER TABLE feeds ADD COLUMN category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL;
CREATE INDEX idx_feeds_category ON feeds(category_id);

-- supports the all-statuses category/uncategorised stream sort-free (see §2)
CREATE INDEX idx_entries_user_pub ON entries(user_id, published_at DESC, id DESC);

-- +goose Down
DROP INDEX idx_entries_user_pub;
DROP INDEX idx_feeds_category;
ALTER TABLE feeds DROP COLUMN category_id;
DROP INDEX idx_categories_user;
DROP TABLE categories;
```
SQLite permits `ALTER TABLE ADD COLUMN` with a `REFERENCES` clause when the column is nullable
with default NULL; the FK is enforced for new and updated rows. `idx_feeds_category` satisfies the
"every child FK column is indexed" invariant (`design.md` §27.17) and serves the grouping/JOIN.
`idx_entries_user_pub` lets the all-statuses stream JOIN walk entries in published order without a
temp B-tree (§2).

### 6.2 sqlc queries
New `queries/categories.sql`:
- `CreateCategory :one`, `GetCategory :one`, `ListCategories :many` (`ORDER BY title COLLATE NOCASE ASC`),
  `UpdateCategory :exec` (rename, scoped by id+user), `DeleteCategory :execrows` (scoped by id+user).
- `UnreadCountsByCategory :many`:
  ```sql
  SELECT f.category_id, COUNT(*) AS n
  FROM entries e JOIN feeds f ON e.feed_id = f.id
  WHERE e.user_id = ? AND e.status = 'unread'
  GROUP BY f.category_id;
  ```
  The row with `category_id IS NULL` is the uncategorised bucket; the store splits it out into the
  `uncategorised int` return value and maps the rest into `map[ID]int`.

Edits to `queries/feeds.sql`:
- `CreateFeed` adds the `category_id` column/param.
- New `SetFeedCategory :execrows` — `UPDATE feeds SET category_id = ? WHERE id = ? AND user_id = ?`
  (0 rows → `ErrNotFound`).
- `GetFeed` / `ListFeeds` are `SELECT *`, so they pick up `category_id` automatically.

`feeds.go`: `feedFromRow` maps `category_id` (`sql.NullInt64` → `*core.ID`) via new `nullID`
(write) / `ptrID` (read) helpers, consistent with `nullUnix`/`ptrUnix` and
`sqlc.yaml: emit_pointers_for_null_types:false`. `CreateFeed`/`SetFeedCategory` wire the param.

**After editing `queries/` or `migrations/`: `make sqlc` and commit the regenerated
`internal/store/sqlite/sqlc/` output; `make sqlc-check` is CI-enforced.**

### 6.3 `ListEntries` (hand-written dynamic SQL in `entries.go`)
Add a conditional JOIN: when `f.CategoryID != nil` or `f.Uncategorised`, alias `entries AS e`,
join `feeds AS f`, and append `f.category_id = ?` or `f.category_id IS NULL` to the WHERE. Column
references and the `ORDER BY`/cursor predicate are prefixed `e.` so they stay unambiguous under
the join. The orderable column stays allowlisted from the closed `Order` switch (never user
input), preserving the existing gosec posture. With no status filter the published-order scan is
served by `idx_entries_user_pub` (§2), so the category stream needs no temp B-tree.

---

## 7. Web (`internal/web`)

### 7.1 Routes
```
GET  /categories                 index: categories + unread counts (+ Uncategorised bucket)
GET  /categories/{id}            aggregated category entry stream (keyset "load more")
GET  /categories/none            uncategorised entry stream
POST /categories                 create (title form field)
POST /categories/{id}/rename     rename (title form field)
POST /categories/{id}/delete     delete (feeds → uncategorised)
POST /feeds/{id}/category        assign/change a feed's category (category_id; empty = uncategorised)
```
`POST /feeds` (subscribe) gains an optional `category_id` form field.

### 7.2 Handlers
- The category stream reuses the generic `renderList` — pass the category title (from
  `CategoryService.Get`) and the filter:
  - `/categories/{id}` → `EntryFilter{CategoryID: &id}`
  - `/categories/none` → `EntryFilter{Uncategorised: true}`
  Load-more, cursor handling, and the HX-Request fragment branch are inherited unchanged.
- `/categories` index handler: `ListCategories` + `UnreadCounts`, rendered into the new template.
- Create/rename/delete: mutate, then re-render the categories index (or its list fragment) for the
  htmx swap, following the existing fragment pattern.
- `/feeds/{id}/category`: parse `category_id` (empty string → nil), call `FeedService.SetCategory`,
  swap the affected feed row (or 204), like the existing toggle handlers.
- Subscribe: read the optional `category_id`, pass it to `Subscribe`.

### 7.3 Templates & nav
- New `templates/categories.gohtml` — index list (title + unread count per category and an
  Uncategorised row) plus the create form and per-row rename/delete controls; registered in
  `parseTemplates`.
- `templates/feeds.gohtml` — group feeds under category headings (sorted, uncategorised last);
  each feed row gains a category `<select>` posting to `/feeds/{id}/category` via htmx, alongside
  the existing refresh/delete controls. The subscribe form gains the category `<select>`.
- `templates/layout.gohtml` — add a **Categories** link to the header nav.

### 7.4 Stream semantics
The category stream mirrors the existing single-feed view (`/feeds/{id}`): **all statuses**,
newest-first. The index **counts are unread** (the actionable number). This count-vs-stream
difference is intentional and consistent with how the feed view already behaves.

---

## 8. Test doubles (`internal/core/coretest/memstore.go`)

`MemStore` is a **behavioral** fake and must not lie (testing convention):
- Add a `categories map[ID]*Category` (+ shared `nextID`) and implement `CategoryStore`.
- `CreateCategory` enforces `UNIQUE(user_id, title)` → `ErrConflict`.
- `UnreadCountsByCategory` computes counts by walking entries → their feed → `CategoryID`.
- `CreateFeed`/`SetFeedCategory` honour `CategoryID`; `DeleteCategory` nils the `CategoryID` of
  feeds that referenced it (mirrors `ON DELETE SET NULL`).
- `ListEntries` honours `CategoryID` (entry's feed's `CategoryID` equals it) and `Uncategorised`
  (entry's feed's `CategoryID == nil`).

---

## 9. Config / CLI / observability
No new env vars, no new CLI subcommand, slog-only (unchanged). OPML category mapping stays deferred.

---

## 10. Testing strategy (TDD: red/green/refactor; stdlib `testing`, fake `Clock`)

- **core:** `CategoryService` CRUD + validation (empty title → `ErrValidation`, dup → `ErrConflict`);
  `FeedService.Subscribe` with a category + ownership rejection of a foreign/bad category id;
  `SetCategory` ownership; `EntryService.List` filtered by `CategoryID` and `Uncategorised`
  (against `MemStore`).
- **store/sqlite (real temp-file DB):** categories CRUD; `UNIQUE(user_id,title)` → `ErrConflict`;
  deleting a category re-homes its feeds to NULL (`ON DELETE SET NULL`); deleting the user cascades
  categories away; `UnreadCountsByCategory` (incl. the NULL/uncategorised bucket); `ListEntries`
  category-JOIN keyset pagination **plus `EXPLAIN QUERY PLAN` asserting index use and no temp
  B-tree**; the uncategorised filter.
- **web (`httptest`):** `/categories` index renders per-category + uncategorised counts; category
  stream lists and "load more" works; create/rename/delete; assign-category row swap; subscribe
  with a category. All scoped to `uid = 1`.
- **Gates:** `make sqlc-check`, `make lint`, `make test-race` all green.

---

## 11. Invariants preserved (`design.md` §27 / `mvp-design.md` §18)
- New tables are `STRICT`; `category_id` is `INTEGER`; the FK column is indexed
  (`idx_feeds_category`); `foreign_keys=ON`.
- Every category/feed/entry query is scoped by `user_id`; no id trusted without its owner.
- Keyset pagination preserved on the category stream — no `OFFSET`, no temp B-tree, via the new
  `idx_entries_user_pub` (§2); asserted by an `EXPLAIN QUERY PLAN` test.
- `internal/core` imports no adapter; ports stay consumer-owned; time-dependent logic in core uses
  the injected `Clock`.
- The migration is additive; MVP tables and data are untouched.

---

## 12. Documentation updates (part of the change)
- `roadmap.md` A6 → `done (iter 3)` for categories (note FTS5 search remains deferred); add both
  category rows to **Done**.
- `design.md` §29 decision log: categories shipped iter 3 — entries filtered via a `feeds` JOIN
  (keyset preserved), explicit uncategorised bucket, unread counts on the index.
- `mvp-design.md` §2 "Organisation: Flat feed list → Categories" reference updated to point here.

---

## 13. Suggested build order (each step leaves the tree green)
1. Migration `0003` + sqlc queries (categories + feeds edits); regenerate sqlc; store tests
   (CRUD, conflict, SET NULL, cascade, counts).
2. `ListEntries` category JOIN + `EntryFilter` fields; store keyset + `EXPLAIN` test.
3. core types/ports + `CategoryService`; `FeedService` subscribe-with-category + `SetCategory`;
   `MemStore` updates; core tests against fakes.
4. web routes/handlers/templates + nav; web handler tests.
5. Docs (roadmap/design/mvp) updates.
