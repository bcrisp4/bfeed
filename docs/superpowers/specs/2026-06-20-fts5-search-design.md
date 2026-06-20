# bfeed — FTS5 Full-Text Search Design (iteration 3)

> Status: approved design, ready for an implementation plan.
> Scope: the **search** half of roadmap iter 3 (`roadmap.md` A5). The categories half shipped
> separately ([`2026-06-20-feed-categories-design.md`](./2026-06-20-feed-categories-design.md)).
> Authoritative specs this builds on: [`design.md`](../../design.md) §4, §8, §9.3, §11, §15, §27,
> §28; [`mvp-design.md`](../../mvp-design.md); tracked in [`roadmap.md`](../../roadmap.md) A5.

---

## 1. Goal & scope

Let the single user **search their entries by text** — type words into a box, get the most
relevant matching entries back. Backed by **SQLite FTS5** over the existing `entries` table.

This is **purely additive**, exactly as `roadmap.md` promises: one new migration (a virtual
table + sync triggers, no change to any base table), a new core port + service, one
hand-written store query, one web route + a nav search box reusing the existing entries
template. **No MVP table, query, or code path is rewritten**, and existing entries are
back-filled into the index by the migration. The app stays single-user
(`core.DefaultUserID = 1`); the search query is still `user_id`-scoped so multi-user remains a
later additive change.

### In scope
- An `entries_fts` external-content FTS5 virtual table over `entries`, indexing **title,
  content, and summary**, kept in sync by canonical insert/delete/update triggers, with a
  one-time backfill of pre-existing rows.
- A `SearchIndex` core port and a thin `SearchService`.
- A safe FTS5 `MATCH` query builder that turns arbitrary user text into a query that **never
  throws a syntax error**.
- Relevance-ranked results (bm25), **capped at 50, no pagination** in this iteration.
- A search box in the layout nav and a `GET /search?q=` results page reusing `entries.gohtml`.

### Out of scope (stays deferred; tracked in `roadmap.md` / `design.md` §28)
Pagination / rank-keyset over results; snippet/highlight excerpts; filtering *within* search
(by feed/category/status/starred); boolean/phrase/`NEAR` operator syntax; **porter stemming**
(stays `unicode61`, per `design.md` §28); htmx live "search-as-you-type". Each is additive on
top of what this iteration builds.

---

## 2. Decisions (the four real choices)

These were settled during brainstorming; the rest of the design follows from them.

### 2.1 Result ordering — relevance, capped at 50, no pagination
Results are ordered by **bm25 relevance** (`ORDER BY rank`), not recency. Search's whole job is
ranking, so a recency order (which every other list already uses) would undersell it.

The cost of relevance ordering is pagination: bm25 `rank` is a float with no stable, monotonic
keyset column, so the `(published_at, id)` cursor the rest of the app relies on does not apply.
Rather than build a `(rank, rowid)` keyset for a single-user reader whose queries are narrow,
this iteration **caps results at 50 and does not paginate**. The UI states the cap when it is
hit. A rank-keyset "load more" is a later additive change, added only if the cap proves to bite.

> **Known limitation (documented intentionally):** a query with more than 50 good matches
> cannot reach result 51+. Mitigation: refine the query. Revisit if it bites in daily use.

### 2.2 Indexed columns — title + content + **summary**
`design.md` §11 specifies indexing `(title, content)`. This design **adds `summary`**.

Rationale: in the parser (`internal/parse/parse.go`), `Entry.Content` is mapped from the feed's
`<content:encoded>` / Atom content and `Entry.Summary` from the RSS `<description>` — **stored
separately, with no fallback**. Many RSS feeds populate only `<description>`, leaving `content`
empty. Indexing only `(title, content)` would make the *body* of every description-only feed
silently unsearchable (title would still match). Indexing `summary` too means search matches
anything the reader can actually read. bm25 can weight `title` above body columns if desired.

### 2.3 Query parsing — per-token quote-and-AND builder, never errors
Raw user text passed straight to FTS5 `MATCH` routinely crashes the FTS5 parser: `c#`,
`node.js`, `it's`, an unbalanced `"`, a lone `*`, or the literal word `OR` all raise
`fts5: syntax error`. Per the FTS5 docs, a double-quoted string is a literal whose only escape
is doubling an embedded `"` (`"` → `""`), and quoting neutralises every special operator
(`* + ^ : - ( ) NEAR AND OR NOT`).

The builder (see §5) therefore: splits input on whitespace; double-quotes each token (escaping
embedded quotes); joins tokens with spaces (**implicit AND** — "all words, anywhere", which is
what a search box implies, *not* a strict adjacent phrase); and appends a `*` to the **last**
token, outside its closing quote, for prefix matching ("type `kuber` → find kubernetes").
Tokens with no indexable characters are dropped; empty input runs no query.

Operator syntax (phrase, boolean, `NEAR`) is **deliberately not exposed** to the raw text box —
both researched sources advise surfacing operators through UI controls instead, if ever wanted.

> Research basis: [SQLite FTS5 docs](https://sqlite.org/fts5.html) (quoting/prefix/`rank`
> rules), [Harold Admin — *Escape your FTS queries*](https://blog.haroldadmin.com/posts/escape-fts-queries),
> [codestudy.net — *Safely Escape Strings for SQLite FTS*](https://www.codestudy.net/blog/how-to-escape-string-for-sqlite-fts-query/).
> The per-token variant is chosen over whole-input quoting because whole-input quoting yields
> strict phrase-match semantics (`rust async` would match only the adjacent phrase), which is
> poor recall for a search box.

### 2.4 UI — plain GET form, reuse the entries list
A search box in the layout nav submits `GET /search?q=…`; the results page reuses the existing
`entries.gohtml` template. No JavaScript, works on a no-JS iPhone, fully boring. htmx
live-search is a later, purely-additive enhancement on the same input + backend.

---

## 3. Schema — migration `0004_fts.sql`

External-content FTS5 (`content='entries'`) stores no duplicate copy of the text; it indexes
the live `entries` rows by `rowid = entries.id`. Three triggers keep it in sync; a final
`'rebuild'` back-fills every row that predates the migration.

```sql
-- +goose Up
CREATE VIRTUAL TABLE entries_fts USING fts5(
  title, content, summary,
  content='entries', content_rowid='id',
  tokenize='unicode61'
);

CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, title, content, summary)
  VALUES (new.id, new.title, new.content, new.summary);
END;

CREATE TRIGGER entries_ad AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content, summary)
  VALUES('delete', old.id, old.title, old.content, old.summary);
END;

-- AFTER UPDATE OF (not bare AFTER UPDATE): re-index only when an INDEXED column
-- changes. read/unread + star toggles and read_at writes — the hot path — never
-- touch title/content/summary, so they must not churn the FTS index.
CREATE TRIGGER entries_au AFTER UPDATE OF title, content, summary ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content, summary)
  VALUES('delete', old.id, old.title, old.content, old.summary);
  INSERT INTO entries_fts(rowid, title, content, summary)
  VALUES (new.id, new.title, new.content, new.summary);
END;

-- MANDATORY: index every entry that predates this migration. Without it,
-- existing entries are invisible to search.
INSERT INTO entries_fts(entries_fts) VALUES('rebuild');

-- +goose Down
DROP TRIGGER entries_au;
DROP TRIGGER entries_ad;
DROP TRIGGER entries_ai;
DROP TABLE entries_fts;
```

**Deviations from `design.md` §11, both deliberate (logged in §29 of the design):**
1. **`+ summary` column** — see §2.2.
2. **`AFTER UPDATE OF title, content, summary`** — the design's bare `AFTER UPDATE` would fire
   the delete+reinsert FTS churn on *every* `entries` update, including the frequent
   read/unread and star toggles that change no indexed text. Scoping the trigger to the indexed
   columns keeps the hot path off the FTS index while staying perfectly in sync (the `'delete'`
   command uses the `old.*` indexed values, which are exactly what was previously indexed).

**STRICT note:** FTS5 creates non-`STRICT` shadow tables (`entries_fts_data`, `_idx`, etc.).
This does not violate invariant 17 — that invariant governs the **base** tables. The spec
records the FTS5 shadow tables as the documented exception.

**sqlc note:** the generated sqlc code reads the migrations as its schema. `make sqlc`
(v1.31.1) must still parse this migration — `CREATE VIRTUAL TABLE … USING fts5(…)` and the
triggers (including the `'delete'`-command `INSERT`, which is ordinary INSERT syntax). **This is
the first thing the implementation plan verifies.** If sqlc chokes, isolate the FTS DDL so it is
applied by goose but excluded from sqlc's analyzed schema. No new `queries/*.sql` are added —
the search query is hand-written (§4), like `ListEntries`.

---

## 4. Store — `Search`

Hand-written raw SQL (not sqlc), mirroring `ListEntries`' approach: the `MATCH` predicate and
the FTS join are not expressible as a static sqlc query. User-scoped, relevance-ordered, capped.

```sql
SELECT e.id, e.user_id, e.feed_id, e.guid, e.url, e.title, e.author, e.content, e.summary,
       e.published_at, e.status, e.starred, e.read_at, e.created_at, e.hash
FROM entries_fts f
JOIN entries e ON e.id = f.rowid
WHERE f MATCH ? AND e.user_id = ?
ORDER BY rank          -- bm25; equivalent to ORDER BY bm25(f) but faster (FTS5 special column)
LIMIT 50;
```

- The `MATCH` argument is the output of `buildMatch` (§5); the `user_id` is bound.
- `ORDER BY rank` returns rows in bm25 order natively — **no temp B-tree**. An
  `EXPLAIN QUERY PLAN` test asserts the FTS5 virtual table drives the join and there is no
  `USE TEMP B-TREE FOR ORDER BY`.
- Tombstoned / deleted entries are absent because the `entries_ad` trigger removes them from the
  index — they can never appear in results (invariant: FTS reflects current `entries`).
- If `buildMatch` returns `""` (no usable token), `Search` returns `(nil, nil, nil)` **without
  touching the DB**.

Signature (the `SearchIndex` port, §6) returns the design's `([]*Entry, *Cursor, error)` shape
for forward compatibility; **this iteration always returns a nil next-cursor** (no pagination).

---

## 5. Query builder — `buildMatch`

A pure function in `store/sqlite` (FTS5 MATCH syntax is a persistence detail; keeping it in the
adapter leaves `core` FTS5-agnostic). Unit-tested with golden cases — no DB needed.

```go
// buildMatch turns raw user text into a safe FTS5 MATCH string. Every token is
// double-quoted (embedded " doubled) so all FTS5 operators are inert; tokens are
// implicitly ANDed; the last token gets a trailing * (outside its quotes) for
// prefix matching. Returns "" when no usable token remains.
func buildMatch(raw string) string
```

| input | output |
|---|---|
| `rust async` | `"rust" "async"*` |
| `node.js` | `"node.js"*` |
| `foo "bar` | `"foo" "bar"*` |
| `OR` | `"OR"*` |
| `c#` | `"c#"*` |
| `   ` (blank) | `""` (empty) |
| `++` (no indexable chars) | `""` (empty) |

Algorithm: `strings.Fields` to tokenize on whitespace; drop tokens that contain no indexable
character (so a lone `++` doesn't become a zero-token quoted phrase, an FTS5 footgun); for each
surviving token replace `"`→`""` and wrap in `"…"`; join with a single space; append `*` to the
final token, after its closing quote.

Golden test cases (assert no `MATCH` error and the expected string): `c#`, `node.js`,
`foo "bar`, `OR` / `AND` / `NOT`, blank, `++`, a single character, CJK (e.g. `日本語`), an emoji,
mixed `Rust  async  "io"`.

---

## 6. Core — port + service

Mirrors `design.md` §8 (`SearchIndex`) and §9.3 (`SearchService`), with one refinement: **core
stays FTS5-agnostic**. The service forwards the trimmed user text and the `user_id` scope; the
adapter (§5) owns the `MATCH` construction. This respects the dependency rule — `core` imports
no adapter and knows no persistence detail.

```go
// internal/core/ports.go
type SearchIndex interface {
    Search(ctx context.Context, userID ID, query string, f EntryFilter) ([]*Entry, *Cursor, error)
}

type Store interface {
    FeedStore
    EntryStore
    CategoryStore
    SearchIndex
}

// internal/core/search.go
type SearchService struct {
    idx SearchIndex
    log *slog.Logger
}
func NewSearchService(idx SearchIndex, log *slog.Logger) *SearchService
func (s *SearchService) Search(ctx context.Context, userID ID, query string, f EntryFilter) ([]*Entry, *Cursor, error)
```

- `SearchService.Search` trims the query; an empty query short-circuits to `(nil, nil, nil)`
  (the empty-results path) without calling the index.
- `EntryFilter` is passed through unused in this iteration — it reserves the additive path for
  filtering search by feed/category/status later, with no signature change.

---

## 7. Web — `/search`

- **Nav search box** in `templates/layout.gohtml`, present on every page:
  ```html
  <form action="/search" method="get" role="search">
    <input type="search" name="q" value="{{.Query}}" placeholder="Search">
  </form>
  ```
  (`{{.Query}}` echoes the current query back; it is empty on non-search pages — the layout view
  model gains an optional `Query` field, defaulting to `""`.)
- **Route:** `GET /search` → `h.search` in `web.New`'s mux.
- **Handler `search`:** reads `q`; if blank, renders the results page with a prompt and no
  query. Otherwise calls `SearchService.Search(ctx, uid, q, core.EntryFilter{})`, builds the
  same `entryVM`s as `renderList` (one `feeds.List` call → feed-title map, as today), and
  renders **`entries.gohtml`** with a search-specific header:
  - `Search: rust async — 12 matches`
  - at the cap (50 results): `Search: rust async — showing first 50 matches; refine to narrow`.
- Reuses the existing `listVM` / entry-row rendering; **no new entry template**. The "load more"
  footer is omitted for search (no cursor). A small `searchVM` (or an extended `listVM` with a
  `Query` + `Capped` flag) carries the header text.

No new env/config. No auth/CSRF change (search is a safe `GET`; tailnet boundary unchanged).

---

## 8. Wiring & composition

- `cmd/bfeed`: construct `core.NewSearchService(store, log)` and pass it into `web.New(...)`
  alongside the existing services. `web.Handler` gains a `search *core.SearchService` field.
- The sqlite `*Store` satisfies the extended `Store` interface once `Search` is implemented; the
  composition root needs no other change.

---

## 9. Testing strategy (TDD)

- **`store/sqlite` (integration, real SQLite):**
  - FTS sync: an inserted entry is searchable; an entry whose title/content/summary is updated
    reflects the new text and not the old; a deleted entry disappears from results; a status/star
    toggle does **not** change search results (proves `AFTER UPDATE OF` scoping).
  - Backfill: entries inserted *before* the migration are searchable after it (exercise via a
    fixture that applies migrations through `0003`, inserts rows, then applies `0004`).
  - Tombstone: a deleted-then-re-polled `(feed_id, guid)` never reappears in results.
  - Ranking: a title hit outranks a body-only hit for the same term.
  - Scope: a query returns only the calling `user_id`'s entries.
  - `EXPLAIN QUERY PLAN`: FTS5 virtual table drives the join; no temp B-tree for `ORDER BY rank`.
- **`buildMatch` (unit, no DB):** the golden table in §5; every case asserts a non-erroring
  `MATCH` against a throwaway FTS5 table and the exact built string.
- **`core` (unit, fakes):** `SearchService` trims input, short-circuits empty queries, forwards
  `user_id`. Extend `coretest.MemStore` with a behavioral `Search` (case-insensitive substring
  match over title/content/summary, AND across whitespace tokens) so `core`/`web` tests need no
  real SQLite — kept honest enough to not "lie" about ranking-independent behavior.
- **`web` (httptest):** `GET /search?q=` routes and renders; scoping to `uid`; blank `q` renders
  the prompt with no store call; the cap header appears at 50 results.

---

## 10. Invariants touched

Upholds `design.md` §27 / `mvp-design.md` §18:
- **§27.11** `entries_fts` always reflects current `entries` (triggers; delete uses old values) —
  now also covers the `summary` column and `AFTER UPDATE OF` scoping.
- **§27.14** every data query is `user_id`-scoped — the search join carries `e.user_id = ?`.
- **§27.17** base tables are `STRICT` — unchanged; FTS5 shadow tables are the documented
  exception (virtual tables cannot be `STRICT`).
- **Architecture (§27.19–20):** `core` imports no adapter; the FTS5 `MATCH` syntax lives in the
  adapter, not the service.

---

## 11. Docs to update on ship

- `roadmap.md`: move A5 "FTS5 search over entries" and "Search UI + route" to **Done (iter 3)**;
  leave "Porter stemming option" deferred. Update the iter-3 row in §C.
- `design.md` §29 decision log: record the `+summary` and `AFTER UPDATE OF` deviations and the
  cap-50 / no-pagination choice.
- `mvp-design.md`: flip the Search row in the §2 scope table.

---

## 12. Implementation order (each step leaves the tree green)

1. **Verify `make sqlc`** parses a draft `0004` FTS migration (de-risk first; see §3 sqlc note).
2. Migration `0004_fts.sql` + `store/sqlite` `Search` + `buildMatch`, with the integration and
   golden tests (§9). Foundation first.
3. `core` `SearchIndex` port + `SearchService`; extend `coretest.MemStore`.
4. `web` `/search` route, handler, nav search box, header; httptest coverage.
5. Wire `SearchService` in `cmd/bfeed`; manual smoke (subscribe → search → hit).
6. Docs (§11); regenerate sqlc if needed (`make sqlc`); `make test-race` + `make lint` green.
