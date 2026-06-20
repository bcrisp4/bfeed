# bfeed — Web UI/UX Redesign Design

> Status: approved design, ready for an implementation plan.
> Scope: a **presentation-layer redesign** of the existing `internal/web` adapter — a coherent
> visual system (type, palette, theming, components) applied across every current view, plus a
> responsive nav and a minimal single-user Preferences page. **No `core`, store, or sanitiser
> change.** The rendered HTML is still the already-sanitised content the web layer renders today.
> Authoritative specs this builds on: [`design.md`](../../design.md) §2 (goals: minimal,
> content-first, serif, light/dark/system, mobile-first, htmx-only, small/fast pages), §18 (Web
> UI), §27 (invariants); tracked in [`roadmap.md`](../../roadmap.md) A11 (theme toggle, settings
> page) and A7.

---

## 1. Goal & scope

Today's web UI is a functional MVP: ~20 lines of CSS, Georgia, one 42rem column, plain blue
links, no visual identity, tiny touch targets, and no handling for over-wide article content
(code blocks/tables blow out the column). This redesign gives bfeed a deliberate, distinctive
look and a comfortable reading + triage experience on desktop **and** phone — without touching
any business logic.

**Design direction — "Reading Desk × Quiet":** a warm, calm, content-first reader whose signature
is the tension between a **warm serif reading voice** (Literata) and a **cool monospace instrument
layer** (IBM Plex Mono — all metadata, nav, labels, counts, captions, code), tied together by one
recurring **claret mark** (`▪`). It deliberately avoids the three generic "AI design" defaults
(cream + terracotta serif; near-black + acid accent; broadsheet hairline columns): warm paper but
a claret accent and a mono instrument layer, generous calm spacing rather than dense newsprint.

### In scope
- A CSS design-token system (color, type, space, radius) driving **three themes** — Light, Sepia,
  Dark — defaulting to the OS preference, with an explicit override persisted in a cookie and
  applied server-side (no flash, works with JS off).
- Self-hosted, embedded web fonts: **Literata** (body + headings) and **IBM Plex Mono**
  (instrument layer), latin-subset woff2, served from `embed.FS` with long cache headers.
- A restyle of every existing template: layout/chrome, entry list rows (with a **summary line**),
  the single-entry **reading view**, feeds, categories, search, starred, history.
- A **responsive navigation**: a thumb-reachable bottom tab bar on phones (with a *More* sheet),
  the same items as a quiet top bar on desktop.
- **Wide-content handling** for article bodies: images/video/iframe, `pre`/code, tables, long URLs.
- A minimal **Preferences page** (`/settings`): theme + list-summary toggle (+ reading width),
  cookie-backed, **no auth** (single-user MVP).
- Derived presentation helpers: **reading time** and the existing relative published time.
- A quality floor: visible keyboard focus, AA contrast in all three themes, `aria-current` nav,
  reduced-motion respected, ≥44px touch targets.

### Out of scope (unchanged; tracked in `roadmap.md`)
Auth / multi-user / per-user settings (the Preferences page is single-user, cookie-only); image
proxy; PWA manifest/icons (can be added later additively); the REST API; any `core`/store/poller
change; FTS ranking or query changes; OPML. No JS framework or build step is introduced — CSS is
hand-written, htmx stays the only client script.

---

## 2. Design tokens

All tokens are CSS custom properties on `:root`, themed by overriding a small color set. Non-color
tokens are theme-independent.

### 2.1 Type
```
--font-serif: "Literata", Georgia, "Times New Roman", serif;   /* reading voice */
--font-mono:  "IBM Plex Mono", ui-monospace, Menlo, Consolas, monospace; /* instrument voice */
```
- **Literata** is used for the wordmark, headings, entry titles, summaries, and article body.
  Variable woff2 (optical-size + weight), plus an italic file. Weights used: 400 / 600 / 700.
- **IBM Plex Mono** is used for everything that is "furniture": nav labels, the unread count,
  per-entry metadata (`feedhost · 2h · 4 min read`), figure captions, code/`pre`, button labels,
  section labels, breadcrumbs. Weights used: 400 / 500 / 600. Furniture text is small, often
  uppercase with positive letter-spacing.

Type scale (rem, fluid only via measure, not viewport units):
```
--fs-display: 1.875rem  (30px)  headings in reading view (clamp down to ~1.5rem on phone)
--fs-h1:      1.5rem
--fs-title:   1rem       entry-row titles (600)
--fs-body:    1.125rem   article body (18px) — list/summary uses 0.875–0.9rem
--fs-meta:    0.6875rem  (11px) mono metadata
--fs-label:   0.625rem   (10px) mono uppercase labels
--lh-body:    1.62;  --lh-title: 1.25;  --measure: 34rem (reading), 40rem (list container)
```

### 2.2 Space / radius / motion
```
--sp-1:.25rem --sp-2:.5rem --sp-3:.75rem --sp-4:1rem --sp-5:1.5rem --sp-6:2rem
--radius:.5rem  --radius-sm:.375rem
--tap:44px      /* minimum interactive target */
--bottombar-h:64px
--ease:160ms ease       /* the only transition; disabled under reduced-motion */
```

### 2.3 Color — three themes
One accent (`--accent`, claret) carries unread markers, the current-nav indicator, chrome links,
primary buttons, and focus rings. A single secondary (`--star`, gold) is reserved **only** for the
starred state. Body article links use `--accent` underlined.

| token | Light | Sepia | Dark |
|---|---|---|---|
| `--bg`         | `#fbfaf7` | `#ece2cf` | `#181613` |
| `--surface`    | `#ffffff` | `#f3ebda` | `#201d18` |
| `--surface-2`  | `#f6f2ea` | `#e6d9c0` | `#211d18` |
| `--ink`        | `#232020` | `#3a3026` | `#e7e0d3` |
| `--ink-soft`   | `#2c2722` | `#473b2c` | `#d8d0c2` |
| `--muted`      | `#8a8278` | `#94866d` | `#978d7e` |
| `--faint`      | `#a59a86` | `#a8987a` | `#8a8071` |
| `--rule`       | `#e8e3d8` | `#ddccae` | `#2a251e` |
| `--accent`     | `#8a2f2f` | `#8a2f2f` | `#d98a6a` |
| `--accent-ink` | `#fbfaf7` | `#f3ebda` | `#181613` |  /* text on accent */
| `--star`       | `#c08a2e` | `#b07d22` | `#e0b25c` |
| `--focus`      | `#8a2f2f` | `#8a2f2f` | `#d98a6a` |

Contrast: every `--ink`/`--muted` on its `--bg`/`--surface` meets WCAG AA for its size; verified
per theme during implementation.

---

## 3. Theming mechanism (default = system, override = cookie, no flash)

- **Default:** CSS ships the Light palette on `:root` and a Dark palette under
  `@media (prefers-color-scheme: dark)`. With no cookie and JS off, the OS preference wins — Light
  or Dark. (Sepia is not an OS preference, so it is only reachable as an explicit choice.)
- **Override:** the Preferences page sets a `bfeed_theme` cookie ∈ `{system,light,sepia,dark}`
  (1-year, `SameSite=Lax`, `Path=/`). The web layer reads it per request and, for an explicit
  non-`system` value, renders `<html data-theme="light|sepia|dark">`. CSS defines
  `:root[data-theme="sepia"]{…}` / `[data-theme="dark"]` / `[data-theme="light"]` overrides that
  win over the media query. Because the attribute is server-rendered, there is **no flash** and no
  JS is required.
- Setting theme is a normal form POST to `/settings` returning the page (htmx-swappable); a tiny
  progressive enhancement may apply it without reload, but the cookie + server render is the
  source of truth.

This satisfies design §18 ("light/dark/system via `prefers-color-scheme` baseline plus a user
toggle persisted in a cookie") and adds Sepia. The web layer already reads wall-clock for
presentation, so reading a request cookie here is consistent with existing layering (no `core`
involvement).

---

## 4. Layout & chrome

- Single column, mobile-first. Container max-width `--measure` list = 40rem, reading = 34rem,
  centered with comfortable gutters. Generous vertical rhythm (not dense).
- **Wordmark:** `bfeed`, Literata 600, `--ink`, slight negative tracking. No accent mark.
- **Header (desktop ≥640px):** wordmark left; horizontal **mono** nav (uppercase, letter-spaced)
  with a claret bottom-border on the current view (`aria-current="page"`); a search field and a
  `Settings` link on the right. No tiny gear icon.
- **Header (mobile <640px):** wordmark only.
- **Bottom tab bar (mobile <640px):** fixed, `--bottombar-h`, four targets — **Unread · Feeds ·
  Starred · More** — each a full-height tap target (≥44px), mono label + simple glyph, active in
  claret. *More* opens a **sheet** (a `<details>`/popover, no framework) listing the overflow:
  **Search, Categories, History, Settings**. The bar is hidden on desktop; its items live in the
  top bar instead. Body gets bottom padding equal to the bar height so content never hides behind
  it.
- Canonical nav set, defined once and rendered responsively:
  - Primary (always one tap): Unread (home), Feeds, Starred.
  - Secondary (More sheet on mobile, inline top-bar on desktop): Search, Categories, History,
    Settings.

---

## 5. Components

### 5.1 Entry-list row (Unread / feed / category / starred / history / search results)
```
▪  Title in Literata 600                                  ← claret square = unread
   feedhost · 2h                                          ← mono meta, --faint
   One- to two-line summary in Literata, --muted,         ← 2-line clamp; toggleable
   clamped so rows stay scannable.
   [actions: ✓ read/unread · ★ star · ⋯ delete]           ← ≥44px targets
```
- **Unread marker:** a small claret `▪`. Read rows drop the marker, dim to ~55%, title weight 400.
- **Meta:** mono, `--faint`; `feedhost` is the feed's host (or feed title where host is unhelpful)
  `·` relative published time (existing `humanizeSince`).
- **Summary:** rendered from the entry's already-sanitised `Summary` (HTML stripped to text for the
  clamp), Literata, `--muted`, `-webkit-line-clamp:2`. Controlled by the **Summary** preference
  (`bfeed_summary` cookie, default **show**).
- **Actions:** the existing htmx star/read/delete controls, restyled as mono buttons / icon
  buttons sized to `--tap`. On desktop they may reveal on row hover; on mobile they are always
  visible and finger-sized. Tapping the **title** opens the reading view.
- **Mark-read-on-open:** opening an entry marks it read (server-side, the existing read action) so
  the unread list self-clears as you read; the row keeps an explicit unread toggle for control.
- **Load more / keyset pagination:** the existing "Load more" htmx control, restyled as a centered
  mono button.

### 5.2 Reading view (single entry)
```
← feedhost                                   claret mono breadcrumb (back to the feed)
Big Literata headline (display size)
author · 14 Jun 2026 · 4 min read            mono byline, --faint
[ Open original ↗ ]                          mono chip, bordered
———————————————————————————————
Article body in Literata, --ink-soft, --lh-body, measure 34rem
(images/code/tables handled per §6)
———————————————————————————————
[ ✓ Mark unread ]  [ Next unread → ]                    [ ★ Star ]
```
- **Reading time:** computed in the web layer from the sanitised content's word count
  (~220 wpm, min "1 min"). Pure presentation helper next to `humanize.go`.
- **Action bar:** sticky-to-bottom-of-content actions; "Next unread →" advances to the next unread
  entry in the current list context (link/htmx; if none, returns to the list).

### 5.3 Feeds / Categories / Search / Starred / History
- **Feeds:** grouped under category headings (already grouped today); each feed card restyled —
  title (Literata), mono URL/host + error state in claret, and the existing controls (category
  select, full-content toggle, refresh, delete) as mono buttons sized for touch.
- **Categories:** index of categories + unread counts (incl. uncategorised), same row idiom; the
  aggregated category stream reuses 5.1.
- **Search:** the search field (mono input), results reuse 5.1 with the query echoed; empty/no-result
  state is an explicit, friendly mono line (not a blank page).
- **Starred / History:** reuse 5.1 (history ordered by read time, as today).
- Empty states everywhere are written as direction, not mood: e.g. Feeds empty → "No feeds yet.
  Subscribe from the home page." in the interface voice.

### 5.4 Subscribe form
Restyled inline form (URL input, category select, full-content checkbox, primary claret button),
mono labels, touch-sized controls.

---

## 6. Wide-content handling (explicit requirement)

Article HTML is already sanitised to a known tag allowlist, so reading CSS scoped to the article
container (`.article`) can target these reliably:

- `img, video, iframe` → `max-width:100%; height:auto;` (never overflow). Figures may break out to
  the full container width while text stays at the 34rem measure; `figcaption` is mono, `--faint`.
- `pre` → a contained box: `--surface-2` background, `1px --rule` border, `--radius`,
  `overflow-x:auto`, `--font-mono`, **no wrap** (horizontal scroll preserves code). Optional small
  mono language label header when a `language-*` class is present. The **page never scrolls
  sideways** — only the box does.
- inline `code` → tinted chip, `overflow-wrap:anywhere` so long tokens wrap.
- `table` → `display:block; max-width:100%; overflow-x:auto;` (the boring, dependency-free scroll;
  wrapping in a scroll `<div>` is the alternative if a table needs full semantics).
- long unbroken URLs / text → `overflow-wrap:anywhere` on body copy and links.
- `blockquote` → claret left border, `--muted`, italic.

These rules live in the article stylesheet and are covered by a visual/fixture check (§9).

---

## 7. Preferences page (`/settings`)

A minimal, single-user, **cookie-backed** settings page — no auth, no DB, no `users` table (those
stay deferred in roadmap A1/A11). Reached from the desktop top bar and the mobile *More* sheet.

- **Theme:** System (default) · Light · Sepia · Dark → `bfeed_theme` cookie.
- **List summaries:** Show (default) · Hide → `bfeed_summary` cookie.
- **Reading width:** Comfortable (default ~34rem) · Wide (~42rem) → `bfeed_width` cookie. *(Optional;
  include if cheap.)*
- `GET /settings` renders the form (current values from cookies). `POST /settings` validates a
  closed enum per field, sets the cookie(s), re-renders. Unknown values fall back to defaults.
- All three settings are read where they apply (layout `data-theme`, list rows, container width).

---

## 8. Assets, fonts & performance

- Fonts embedded in `internal/web/static/fonts/` via the existing `embed.FS`; served with
  `Cache-Control: public, max-age=31536000, immutable` and a content hash or version in the path.
- Ship: Literata variable (roman) woff2 + Literata italic woff2 + IBM Plex Mono (400/500/600)
  woff2, **latin-subset** to keep total added weight modest (target ≲200KB). `font-display: swap`
  with the Georgia / system-mono fallbacks so text paints immediately; `<link rel="preload">` the
  two most-used faces (Literata roman, Plex Mono 400).
- CSS stays hand-written (split into a few files or one `app.css`); no bundler, no build step;
  served from `embed.FS` with cache headers. htmx remains the only JS.
- Honors design §2 "small pages, fast on poor connections": fonts are cached immutably and
  subsetted; the critical render needs no JS and no network round-trip for theme.

---

## 9. Testing

Presentation layer, so tests defend structure and rules, not pixels:
- **`web` handler tests (`httptest`)**: each view renders; `data-theme` reflects the `bfeed_theme`
  cookie (system → no attribute; explicit → correct attribute); `aria-current` marks the active
  nav item; the summary toggle cookie shows/hides the summary block; `/settings` POST sets cookies
  and rejects out-of-enum values; unread marker present only on unread rows.
- **Reading-time helper**: unit tests (word-count buckets, min "1 min"), like `humanize_test.go`.
- **Wide-content fixture**: a golden article fixture containing a wide `pre`, a wide `table`, an
  oversized `img`, and a long URL renders within the article container and the CSS rules that
  contain them are present (assert the classes/structure; a manual/visual check confirms scroll).
- **Accessibility smoke**: focusable controls have visible focus styling; nav has `aria-current`;
  tap targets meet `--tap` (assert via class/structure).
- No `core`/store tests change; the sanitiser invariant (safe HTML before persistence) is untouched
  — the web layer still renders only already-sanitised content as `template.HTML`.

---

## 10. Affected files (presentation only)

```
internal/web/
  templates/layout.gohtml     header + responsive nav + bottom bar + <html data-theme> + font preload
  templates/_nav.gohtml        (new) the canonical nav set, rendered for top bar + bottom bar + More sheet
  templates/entries.gohtml     subscribe form + list container restyle
  templates/rows.gohtml        entry row: marker, serif title, mono meta, summary, touch actions
  templates/entry.gohtml       reading view: breadcrumb, headline, byline+reading-time, article, action bar
  templates/feeds.gohtml       feed cards restyle
  templates/categories.gohtml  restyle
  templates/search.gohtml      restyle + empty state
  templates/settings.gohtml    (new) Preferences page
  static/app.css               full rewrite to the token system + components + article rules
  static/fonts/…               (new) Literata + IBM Plex Mono woff2 (latin subset)
  handlers.go                  /settings GET+POST; read theme/summary/width cookies; pass to templates
  web.go                       wire /settings route; cache headers for fonts
  reading_time.go              (new) word-count → "N min read" helper (+ test)
```
No change to `cmd/bfeed`, `internal/core`, `internal/store`, `internal/sanitize`, or any port.

---

## 11. Invariants preserved

- §27.1–2 (sanitise-before-persistence; safe HTML): untouched — the web layer renders only the
  already-sanitised `Content`/`Summary` as `template.HTML`; this redesign changes CSS/markup
  around it, never what is stored or how it is sanitised.
- §27.21 (injected `Clock` in core): unaffected — the web presentation layer continues to read
  wall-clock for relative time, and now a request cookie for theme; `core` is not involved.
- §27.19–20 (dependencies point inward; core owns interfaces): no new core dependency; this is a
  driving-adapter restyle only.
- Keyset pagination, user-scoping, and all data paths are unchanged.

---

## 12. Decisions captured during design

- Direction: blend of "Reading Desk" (warm serif + mono instrument layer) and "Quiet" (calm, airy,
  low-contrast) — rejected a dense terminal/instrument list and the pure-broadsheet look.
- Theme: **default follows the OS**; explicit Light/Sepia/Dark override lives on a **Preferences
  page**, not in the header (the header stays calm). Cookie-persisted, server-rendered, no flash.
- Type: **Literata** body/headings + **IBM Plex Mono** instrument layer; both embedded/self-hosted
  (chosen over system fonts for cross-device consistency and a tuned identity; Georgia/system-mono
  remain the swap fallbacks).
- Wordmark: plain ink, no accent mark.
- Unread marker: claret `▪` square (the one recurring mark of the system).
- Summary line under each list title, with a Show/Hide preference (default Show).
- Mobile nav: thumb-reach bottom tab bar + *More* sheet; the top gear icon is removed.
- Explicit wide-content rules promoted to a first-class requirement (§6).
