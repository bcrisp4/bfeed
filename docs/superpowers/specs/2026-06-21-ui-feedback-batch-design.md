# Design: UI feedback batch (entry actions, reader, mobile, search, add-feed)

Status: approved 2026-06-21. Scope: a batch of UI/UX fixes and one bug cluster
raised against the current web UI. No backend/domain changes beyond web handlers
and templates; no schema, store, or core-service changes.

## Background

Feedback raised against the htmx web UI:

1. Entry-list action buttons (mark read / star / delete) look heavy; the star
   glyph is tiny.
2. Mobile bottom-bar icons are too small.
3. Mark-read sometimes needs several presses.
4. Stale frontend state after Back: open an unread entry, read it, hit Back — the
   entry still shows unread, and the first mark-read press toggles the *wrong*
   way.
5. An opened entry is not reflected as read when returning to the unread list.
6. Search page shows redundant "how to search" instructions.
7. No way to star (or otherwise act on) an entry from the reader view.
8. Adding a feed redirects to Unread; it should stay on the feeds page and show
   the new feed.
9. The search button overflows the content column / appears wider than the top
   bar.

Items 3, 4, 5 share a single root cause and are fixed together (section A).

## A. Bug cluster — stale read-state after Back navigation

### Root cause

List pages (`/`, `/starred`, `/history`, `/feeds/{id}`, `/categories/...`) are
served with no cache directives, so Safari (and other browsers' bfcache) restore
a **cached DOM** on Back/Forward. Opening an entry marks it read server-side
(`handlers.go` `entry` → `MarkRead`), but the restored list page still shows the
entry as unread.

The row's mark-read control is a **toggle** whose action is computed from fresh
*server* state (`toggleRead`: `read := cur.Status != core.StatusRead`). Against a
stale "unread" label the server already says read, so the first press marks it
*unread* (looks like nothing happened), and a second press marks it read. Hence
"press multiple times" and "wrong direction first."

### Fix

1. **Defeat bfcache restore on Back/Forward.** Add a tiny inline script in the
   layout:
   ```js
   window.addEventListener('pageshow', function (e) {
     if (e.persisted) window.location.reload();
   });
   ```
   `event.persisted` is true only when the page is restored from the
   back/forward cache, so this reloads exactly when state may be stale and is a
   no-op on normal navigation (no added latency forward).
2. **Belt-and-suspenders HTTP caching.** Send `Cache-Control: no-store` on
   dynamic HTML responses (everything except `/static/`). Implement as
   middleware that sets the header before `next.ServeHTTP`; the existing
   `cacheStatic` handler still overrides it for `/static/` assets (it sets its
   own `Cache-Control` inside the handler, which wins). This also stops the
   browser reusing a stale list from the HTTP cache.
3. **Prevent the double-fire race.** Add `hx-disabled-elt="this"` to the
   per-row action buttons so a button is disabled while its POST is in flight,
   preventing a rapid double-click from sending two toggles (net no-op).

### Result

Returning to Unread refetches the list → read entries fall out of the unread
filter (they disappear), and every visible row's toggle matches server state, so
a single press works. Fixes items 3, 4, 5.

### Tradeoff (accepted)

Back to a list resets scroll position (a full reload). Acceptable for a
single-user reader. Not preserving scroll this iteration.

## B. Entry-list action buttons (locked design)

Replace the three text/glyph bordered buttons with **ghost icon buttons** using
inline SVG icons:

- Layout: `.actions` is a flex row; each button is borderless, transparent
  background, `width/height: 2.75rem` (44px tap target), `border-radius: .55rem`,
  default colour `var(--muted)`. Icon SVG is `1.5rem` (24px), `currentColor`.
- Hover/focus feedback is **icon-only** (no background tint — that read as messy
  and was invisible in dark theme where `--surface-2 ≈ --surface`):
  - Mark-read tick: bare check when unread; **check-in-circle** when read. On
    hover/focus the icon **flips to preview the result** (unread row previews the
    circle; read row previews the bare check).
  - Star: **outline** when not starred, **filled `var(--star)` gold** when
    starred. On hover/focus it previews the toggle (empty → gold fill; gold →
    empty outline).
  - Delete: colour → `var(--accent)` on hover/focus.
- Keyboard: `:focus-visible` shows a 2px `var(--focus)` ring (the only non-icon
  cue, needed for a11y since there's no hover background).
- Every button has both a `title` (tooltip) and `aria-label`; labels reflect the
  *action* and flip with state (Mark read ⇄ Mark unread, Star ⇄ Unstar).
- The `▪` unread dot in the title stays as the at-a-glance status marker.

The mark-read tick uses two stacked SVGs (`.on-unread` bare check, `.on-read`
check-in-circle) toggled by CSS on the row's read state and on hover/focus, so no
JS is needed for the icon flip.

Icon set (inline SVG, stroke `currentColor`):
- check: `M4 12.5l5 5 11-11`
- check-in-circle: check path + `circle r=10`
- star: standard 5-point star path, `fill:none` (outline) / `fill:currentColor`
  (filled)
- trash: `M4 7h16 M9 7V4h6v3 M7 7l1 13h8l1-13`

## C. Reader-view actions (locked — full set)

The reader (`entry.gohtml`) gains a quiet action bar between the byline and the
article: **Star + Mark unread + Delete**, using the same ghost icons as the list,
with "Open original ↗" moved to the right end of that bar (separated by a hairline
rule top and bottom).

- The reader opens already marked read (existing behaviour), so the tick shows
  the read state and previews mark-unread on hover.
- Star toggles via the existing `POST /entries/{id}/star`.
- Mark unread via the existing `POST /entries/{id}/read` toggle.
- Delete via the existing `POST /entries/{id}/delete`, keeping the
  `hx-confirm="Delete this entry?"`. After delete the reader has no entry to
  show, so the handler responds `HX-Redirect: /` (Unread) and htmx navigates
  there. `deleteEntry` must distinguish the reader caller from the list caller
  (the list row swaps `hx-swap="delete"` and needs no redirect): gate the
  `HX-Redirect` on a request signal from the reader button (e.g. an
  `hx-vals`-supplied `from=reader` field, or a distinct header), leaving the
  list-row delete behaviour unchanged.

Note: the list-row and reader actions share the same icon markup and CSS; factor
the icon SVGs into a reusable template block (e.g. `{{define "icon-star"}}` …)
referenced by both `rows.gohtml` and `entry.gohtml` to avoid duplication.

## D. Mobile bottom bar

Replace the text glyphs (`▪ ≡ ★ ⋯`) in `_nav.gohtml`'s `bottombar` with 24px
inline SVG icons; labels stay at their current small size:

- Unread = target (concentric circles), Feeds = three lines, Starred = star,
  More = horizontal dots.
- `.bottombar .tab svg { width: 1.55rem; height: 1.55rem }`.
- `aria-current="page"` colouring unchanged.

The "More" overflow sheet (Search / Categories / History / Settings) stays as
text links — no icons needed.

## E. Search page

In `search.gohtml`:
- Remove the empty-state instruction line ("Type a query to search your
  entries."). With nothing typed, show nothing below the form (the autofocused
  input invites typing). Keep the "No entries match your search." empty result
  message and the "Search: … — N matches" header (those are results, not
  instructions).

In `app.css`:
- Fix the search form overflow: the `.search` flex input needs `min-width: 0` so
  it can shrink below its intrinsic size, keeping the input + submit button within
  the content measure on all viewport widths. Verify against the running app at
  narrow widths (the button must not extend past the top bar). Constrain the form
  to the content width if needed.

## F. Add-feed flow

Change `subscribe` (`handlers.go`) and the subscribe form (`feeds.gohtml`) so a
successful subscribe stays on the feeds page and shows the new feed:

- Success: respond `HX-Refresh: true` + `204` (matches the existing
  bulk-mutation convention used by `markFeedRead` / `setFeedFullContent`). htmx
  reloads the current page (`/feeds`), which now lists the new feed (Subscribe
  already does one synchronous poll, so the feed is populated).
- Failure: instead of swapping raw `http.Error` text into `<body>`, render a
  small error banner into a dedicated message target and keep the typed URL.
  - Form: `hx-post="/feeds" hx-target="#subscribe-msg" hx-swap="innerHTML"`, with
    a `<div id="subscribe-msg" role="alert"></div>` below the form.
  - Handler on error: write the error banner fragment with HTTP `200` (htmx does
    not swap 4xx/5xx by default), e.g.
    `<p class="form-error">Couldn't subscribe: …</p>`.
  - Add a `.form-error` style (accent-coloured, mono, small).

The existing top-level redirect to `/` is removed.

## Out of scope (unchanged)

- No changes to core services, store, schema, sqlc queries, or the poll/scrape
  pipelines.
- Feed rename / user-overridable title (roadmap A7) — untouched.
- Search pagination — still capped at top-50, unchanged.
- Scroll-position preservation on Back — explicitly deferred.

## Testing

Per project TDD conventions (stdlib `testing`, no testify; `web_test` external
package using `coretest` doubles):

- **Add-feed:** assert a successful subscribe responds `204` with
  `HX-Refresh: true` (not a `303` to `/`); assert a failed subscribe responds
  `200` with the error fragment and does not refresh.
- **No-store header:** assert dynamic HTML responses carry
  `Cache-Control: no-store` and `/static/` responses keep their existing cache
  headers.
- **Reader actions:** assert the reader page (`GET /entries/{id}`) renders star,
  mark-unread, and delete controls.
- **Search empty state:** assert the no-query search page does not contain the
  removed instruction text but the form is present.
- Existing `toggleRead`/`toggleStar`/`deleteEntry` handler behaviour is
  unchanged; their tests stay green.
- Template/markup changes (icons, bottom bar, CSS) are verified by running the
  app (manual + Playwright screenshots across Light/Sepia/Dark and a phone
  viewport) since they are presentational.

## Changelog

Per repo policy, add entries under `[Unreleased]` (user-facing):
- Fixed: entries opened from a list now reliably show as read after navigating
  back; mark-read works on the first press.
- Changed: redesigned entry action buttons (clearer icons, larger star, bigger
  tap targets); larger mobile navigation icons.
- Added: star, mark-unread, and delete controls in the reader view.
- Changed: adding a feed now stays on the Feeds page and shows the new feed.
- Removed: redundant instructions on the search page; fixed the search form
  overflow.
