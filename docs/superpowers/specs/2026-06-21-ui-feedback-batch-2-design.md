# UI feedback batch 2 — design

> Status: approved design, ready for an implementation plan.
> Scope: a batch of small, mostly-independent web-UI fixes and enhancements on top of the
> current htmx UI. No new domain behaviour; one new read-only store query.
> Companion docs: `docs/design.md` (§18 Web UI), `docs/roadmap.md`.

## Summary

Eight items from a user feedback batch, grouped by the kind of change they need:

- **Group A — CSS/template only** (no Go): reader-star not updating, bottom-bar icon padding,
  circled-tick icon size, whole-row click target.
- **Group B — empty states**: explicit "nothing here" messaging on empty lists (template +
  one VM field).
- **Group C — data**: date format change + hover tooltip, and per-feed unread/total counts
  (one new sqlc query, a service passthrough, and view wiring).

All changes are additive and preserve existing invariants (every user-facing query stays
`user_id`-scoped; keyset pagination, sanitisation, no-store caching, and the `Clock` rules are
untouched). Each item carries its own test.

---

## Group A — CSS / template only

### A1. Reader star does not update when clicked (bug)

**Symptom:** in the reader view, clicking the star toggles the star server-side (the state is
correct after a manual reload) but the icon does not change in place.

**Diagnosis:** the server side is already correct — `TestReaderStarReturnsStarFragment` proves
the handler toggles the star and returns the `readerstar` fragment with the updated
`aria-label`. So the defect is the client-side htmx swap. The reader star is the only action in
the UI that does `hx-swap="outerHTML"` **targeting itself** (`hx-target="#reader-star"` on the
button that also carries the id). Entry-list rows target their **parent** row; reader
mark-unread/delete use `hx-swap="none"`. Self-replacing `outerHTML` is the outlier.

**Fix:** swap a stable container instead of the element itself — the same "replace a wrapper,
not yourself" shape the list already uses.

- In `entry.gohtml`, wrap the star in a stable element that owns the id:
  `<span id="reader-star" class="reader-star-slot">{{template "readerstar" .Entry}}</span>`.
- The button inside targets that wrapper: `hx-target="#reader-star" hx-swap="innerHTML"`.
- The `readerstar` define returns **only the `<button>`** (the id moves to the wrapper).
- `readerToggleStar` is unchanged — it already renders the `readerstar` fragment.

**Verification:** reproduce in a real browser (`make run` + Playwright) to confirm the swap now
updates in place before and after the change. If in-browser repro reveals a different root
cause, fix that instead — the wrapper pattern is the robust target regardless.

**Tests:** keep a handler test asserting the toggled fragment (update
`TestReaderStarReturnsStarFragment`: the response still contains `aria-label="Unstar"` and the
star button; the `id` now lives on the wrapper rendered by the page, so assert on the button +
aria-label rather than on `id="reader-star"` being in the fragment). Add an assertion that the
reader page renders the `#reader-star` wrapper around the button.

### A2. Bottom-bar icons too close to the bar edge (mobile)

The fixed bottom bar is `--bottombar-h: 64px` and centers its tabs with no vertical padding, so
the icons hug the top/bottom edge.

**Fix (CSS only, in the `max-width:639px` block):**

- Give `.bottombar` vertical breathing room: `padding-block` (a few px top, and
  `max(<pad>, env(safe-area-inset-bottom))` at the bottom so the iOS home indicator is cleared).
- Bump `--bottombar-h` so the `body` bottom clearance — `calc(var(--bottombar-h) + var(--sp-5))`
  — keeps pace with the taller bar.
- Keep each tab's tap target ≥ `--tap` (44px).

### A3. Circled-tick "read" icon looks oversized next to star/delete

`ic-check-circle` draws its circle at `r=10` in a 24×24 box (~83% of the frame); the star and
trash glyphs only fill ~67–72%, so the read icon reads larger than its neighbours.

**Fix:** reduce the circle radius in `ic-check-circle` to `r≈9` (and nudge stroke if needed) so
it optically matches the star/trash. The bare-check (unread) variant `ic-check` is unchanged.
Confirm in-browser that the three icons now look the same visual size in both the list `.actbar`
and the reader `.reader-actions`.

### A8. Whole entry row is clickable, not just the title

Today only the title `<a>` opens the entry. Make the whole list card a click target with a
CSS **stretched link** (no JS), scoped so it does **not** affect the shared `.entry` class used
on the feeds/categories pages.

**Fix (CSS, scoped to `#entries` — the entry-list/search container):**

- `#entries .entry { position: relative }`
- `#entries .entry h2 a::after { content:""; position:absolute; inset:0 }` — the title anchor
  now covers the whole card.
- `#entries .entry .actbar { position: relative; z-index: 1 }` — action buttons stay clickable
  above the overlay.

Notes:
- Scoping to `#entries` is essential: the feeds page and categories page reuse `.entry` but are
  **not** inside `#entries`, so they are unaffected. The swapped `entryrow` fragment lands
  inside `#entries`, so it inherits the behaviour automatically.
- Hover affordance comes for free: hovering anywhere on the card hovers the anchor, so the
  title underlines (existing `a:hover` rule). No new hover background needed.
- Accessibility unchanged: the real anchor is still the title, so keyboard and screen-reader
  navigation are identical.
- In the list-row meta the feed title is plain text (not a link), so there is no nested-anchor
  conflict. (The reader view is not a list row and is out of scope for this.)

---

## Group B — empty states (B5)

When a list has no items the page is currently blank, which is ambiguous ("is it empty or did it
fail to load?"). Add an explicit, calm empty state, mirroring the search page's existing pattern.

**Mechanism:**

- Add `Empty string` to `listVM`, set per view in the handler.
- In `entries.gohtml` content: `{{if .Entries}}{{template "entrylist" .}}{{else}}<empty
  state>{{end}}`. This lives in the **content block**, never the `entrylist` fragment htmx
  re-renders on "load more", so it can't be duplicated or lost during pagination.
- Add a styled `.empty-state` block (quiet, centred, calm — a headline plus an optional faint
  subline).

**Copy** (deliberately avoids the internal words "entry"/"entries"; plain and neutral):

| View | Headline | Subline (optional) |
|---|---|---|
| Unread (home) | You're all caught up. | — |
| Starred | Nothing saved yet. | Tap the star to keep things here. |
| History | Nothing read yet. | — |
| Single feed | Nothing here yet. | — |
| Category / Uncategorised | Nothing here yet. | — |

Also update the search page's existing empty copy from "No entries match your search." to
**"Nothing matches your search."** for consistency, and restyle its `.empty` paragraph with the
same `.empty-state` treatment.

---

## Group C — data changes

### C4. Date format: hybrid relative/absolute + hover tooltip

Decided behaviour:

- **`< 24h`:** keep relative — `just now`, `Nm ago`, `Nh ago` (recency signal).
- **`≥ 24h`:** absolute date `2 May 2026` (Go layout `"2 Jan 2006"`), replacing both the old
  `Nd ago` and the ISO `2006-01-02` fallback.
- **Always:** a hover tooltip showing the full date **and** time.

**Mechanism:**

- `humanizeSince` (in `humanize.go`): `<1m` → "just now"; `<1h` → "Nm ago"; `<24h` → "Nh ago";
  else `t.Format("2 Jan 2006")`. Update `humanize_test.go` for the new boundaries/format.
- Render the date as a semantic `<time>` element carrying the tooltip:
  `<time datetime="{{.PublishedAttr}}" title="{{.PublishedFull}}">{{.Published}}</time>` in both
  the list-row meta (`rows.gohtml`) and the reader meta (`entry.gohtml`).
- Add to `entryVM`: `PublishedFull` (e.g. `t.Format("2 Jan 2006, 15:04")` for the `title`) and
  `PublishedAttr` (RFC3339 for the machine-readable `datetime`). Populate in `toEntryVM`.
- Timezone: the store holds UTC instants; the tooltip formats that stored instant directly. No
  per-user timezone handling — out of scope for the single-user self-host. (`humanizeSince`
  already compares against wall-clock `time.Now()`, which is allowed in the web layer.)

### C6 + C7. Per-feed unread and total counts

The user wants: an unread count on the **unread view** and the **single-feed view**, and both an
**unread** and a **total** count next to each feed on the **feed-management** page. One combined
query serves all three.

**Store (new sqlc query, static SQL → `make sqlc`):**

```sql
-- name: EntryStatsByFeed :many
SELECT feed_id,
       COUNT(*)                                  AS total,
       COUNT(*) FILTER (WHERE status = 'unread') AS unread
FROM entries
WHERE user_id = ?
GROUP BY feed_id;
```

`COUNT(...) FILTER` returns a non-null `INTEGER`, so sqlc maps `total`/`unread` to `int64`
cleanly (no nullable/`interface{}` surprises from `SUM(CASE …)`). Feeds with zero items return
no row; map lookups yield the Go zero value → "0 unread · 0 total".

- New core type `FeedEntryStats { Total, Unread int }`.
- New `FeedStore` port method `EntryStatsByFeed(ctx, userID ID) (map[ID]FeedEntryStats, error)`,
  implemented in `store/sqlite` (mirrors the `UnreadCountsByCategory` shape).
- New `FeedService` method exposing it to the web layer.
- Mirror the behaviour in `coretest.MemStore` (it is a behavioural fake — keep it in sync, or
  tests pass against a fake that lies).

**Web wiring:**

- **Feed-management page (`/feeds`):** add `Unread int` / `Total int` to `feedRowVM`; populate
  from the stats map in `listFeeds`. Render in the row meta as quiet mono text, e.g.
  `… · 5 unread · 312 total`.
- **Unread view (`/`):** show total unread (sum of the map's `Unread`) in the header.
- **Single-feed view (`/feeds/{id}`):** show that feed's unread (and total) in the header.

`renderList` is shared by several views; only the unread and single-feed views show a header
count, so the stats query runs only for those (not on starred/history/category lists). Counts
render as quiet `.meta`-style mono text in the `.list-header`, not loud badges — in keeping with
the calm, content-first chrome.

---

## Out of scope / non-goals

- No timezone/locale handling for dates (single-user self-host; store is UTC).
- No counts in the top nav / bottom bar (only the three places requested).
- No changes to core domain behaviour, scheduling, scraping, or persistence invariants.

## Testing

- **A1:** updated reader-star handler test + a reader-page test asserting the `#reader-star`
  wrapper; in-browser repro to confirm the visual swap.
- **A2/A3/A8:** CSS — verified in-browser (and the existing `TestEntryRowHasIconActions` /
  `TestIconsRenderInBottomBar` still pass; A8 may add an assertion that `#entries` rows carry the
  stretched-link markup if it is template-visible).
- **B5:** handler/template tests asserting the empty-state copy renders when a list is empty and
  is absent when it has items.
- **C4:** `humanize_test.go` boundary/format cases; a template test asserting the `<time
  title=…>` tooltip renders.
- **C6/C7:** `store/sqlite` integration test for `EntryStatsByFeed` (per-feed total + unread,
  zero-item feed, user scoping); web tests asserting the counts render on `/feeds`, `/`, and
  `/feeds/{id}`.

## Process notes

- After editing `queries/`, run `make sqlc` and commit the regenerated `sqlc/` code
  (CI runs `make sqlc-check`).
- Add one `[Unreleased]` entry to `CHANGELOG.md` under `Fixed` (reader star, icon sizing) and
  `Added`/`Changed` (counts, date format, empty states), from the user's perspective.
- Run `make test-race` and `make lint` before declaring done.
