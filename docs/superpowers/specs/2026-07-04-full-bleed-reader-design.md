# Full-bleed images, figures and tables in the reader view

**Date:** 2026-07-04
**Issue:** [#84](https://github.com/bcrisp4/bfeed/issues/84)
**Status:** approved

## Goal

In the reader view, prose keeps its 34rem measure while selected wide content
(figures, images, tables) is allowed to "bleed" past the article column, up to
a clamped wide tier. Text stays readable; media that benefits from width gets
room to breathe.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Bleed set | `figure` (+`figcaption`), `img` (wrapped), `table` | `pre` excluded: wide code slabs dominate a calm serif page, payoff small (most lines fit the measure), and it is one selector away if wanted later. |
| Width tier | Clamped wide: `min(56rem, viewport)` (~1.6x measure), centered | Editorial consensus (Medium-style tiers): clamped "wide" beats edge-to-edge for inline figures; true full-bleed is a hero-image treatment. On phones every option collapses to edge-to-edge anyway. |
| Technique | Negative `margin-inline` clamped with `min()/max()`, `50vw` viewport term, `overflow-x: clip` backstop on `html` ("approach A") | See "Approach" below. |
| Upscaling | Never. Widen the *slot*, cap the image at intrinsic size | Small/badge images from feeds must not blur. `inline-size: auto; max-inline-size: 100%` renders intrinsic size up to the slot cap. |
| Scope | `internal/web/static/app.css` + one wrapper div in `entry.gohtml` | Additive; no sanitiser, store or route changes. Embeds (`iframe`/`video`) remain stripped — out of scope per issue. |

## Approach

Bleed elements get symmetric negative `margin-inline` computed from two
clamps and escape their ancestors' boxes via ordinary overflow (nothing
between `.article` and the viewport sets `overflow`):

```css
margin-inline: min(0px, max(50% - var(--measure-wide)/2, 50% - 50vw));
```

- `50% - 28rem` — the 56rem wide tier (both terms compete in `max()`,
  less-negative wins).
- `50% - 50vw` — viewport edge. On phones this escapes the body's 1rem
  padding: true edge-to-edge.
- `min(0px, ...)` — never narrower than the column.
- Logical properties throughout — RTL correct for free.
- Width-auto blocks fill their parent at every depth, so the `50%` basis is
  identical wherever the figure sits — the math survives feed HTML's
  arbitrary `<div>` nesting.

### Why not the container-query (`cqi`) approach the issue suggested

`container-type: inline-size` implies **layout containment**, which makes the
container the containing block for `position: fixed` descendants. bfeed's
mobile `.bottombar` is `position: fixed` inside `body` — making `body` or
`html` a container re-anchors the bar to the document instead of the
viewport (it scrolls away). Additionally, bfeed's `body` *is* the capped
40rem column, not a full-width wrapper, so `100cqi` on it would measure
~38rem, never the page. Using `cqi` correctly would require restructuring the
layout of every page (uncapped body, a full-width `.page` container wrapping
appbar/nav/main but not the bottombar) — a whole-app layout migration whose
only concrete win over approach A is removing a <=8px edge shave that occurs
solely on classic-scrollbar desktops at 640-950px window widths. Bad trade.
If it ever hurts, the restructure can be done later and the bleed formula
just swaps `50vw` for `50cqi`; selectors, clamps and fixtures carry over.

Also rejected: "wide article, inset prose" inversion (Comeau-style) — tables
solve elegantly but only *direct* children of `.article` can be styled, so
nested media never bleeds, and the rules would touch every element of
untrusted HTML instead of just the media set.

### Known wart (accepted)

`100vw` includes a classic (space-occupying) scrollbar. When the viewport
term binds (window narrower than ~950px) on a classic-scrollbar system, a
bled element computes ~15px too wide and `overflow-x: clip` on `html` shaves
~7px per side off the image edges. Phones use overlay scrollbars
(unaffected); wide desktop windows use the 56rem term (unaffected). Cost is a
few pixels of picture edge in one corner case — no layout breakage, no
horizontal scrollbar. Secondary cost: `html { overflow-x: clip }` masks
future accidental horizontal-overflow bugs as clipped content rather than a
visible scrollbar.

## Design

### Template

`entry.gohtml`: wrap the content block in `<div class="reader">`. Scopes all
new CSS to the reader page.

### CSS (append to reader section of `app.css`)

```css
:root { --measure-wide: 56rem; }

/* reader column: center article, cap+center header siblings.
   Prerequisite: .article is left-aligned in the 38rem body content box
   today, which would put symmetric bleed ~2rem off-center. */
.reader > * { max-inline-size: var(--measure); margin-inline: auto; }

/* neutralise UA figure margins (margin: 1em 40px) */
.reader .article figure { margin-inline: 0; }

/* widen the slot; img stays inline content, so text-align centers it
   without flex -- mixed text+img paragraphs degrade to centered text,
   not stacked blocks */
.reader .article :is(figure:has(img), p:has(> img:only-child), div:has(> img:only-child)) {
  margin-inline: min(0px, max(50% - var(--measure-wide)/2, 50% - 50vw));
  text-align: center;
}

/* never upscale: intrinsic size up to slot width */
.reader .article img { inline-size: auto; max-inline-size: 100%; block-size: auto; }

/* captions readable-width, centered under a bled image */
.reader .article figcaption { max-inline-size: var(--measure); margin-inline: auto; }

/* tables: widen slot, center narrow tables, scroll monsters.
   flex column is needed because a wrapperless table cannot otherwise be
   centered-when-narrow AND scrollable-when-wide; align-items fallback-first
   for browsers without `safe`. */
.reader .article table {
  margin-inline: min(0px, max(50% - var(--measure-wide)/2, 50% - 50vw));
  display: flex; flex-direction: column;
  align-items: center; align-items: safe center;
  overflow-x: auto;
}

html { overflow-x: clip; } /* scrollbar-overshoot backstop */
```

Note: `.reader > *` centering caps h1/meta/readerbar at the measure (today
they run 38rem left-aligned). Visible change: the header block aligns exactly
with article text. Deliberate polish; shows in before/after screenshots.

### Behaviour matrix

| Content | Result |
|---|---|
| 320px screenshot in figure | intrinsic 320px, centered — unchanged |
| 1600px photo in figure | fills up to 56rem or viewport, centered |
| `<p>text with inline img</p>` | excluded (`:only-child`) — unchanged |
| gallery `<p><img><img></p>` | excluded — unchanged (v1) |
| narrow table | shrinks to content width, centered |
| wide table | grows to 56rem, scrolls inside the bled slot |
| bare `<img>` direct child of `.article` | centered at measure, no bleed (rare; accepted gap) |

### Known heuristic risks and fallbacks

- `p:has(> img:only-child)` still matches a paragraph whose only *element*
  child is the img but which also contains text nodes — that text gets
  centered (cosmetic). Fallback: drop `p` from the selector set.
- flex-on-`table` is the least battle-tested rule (needed because feed
  tables have no wrapper). Fallback: revert tables to today's
  block-scroll-at-measure and ship image/figure bleed only.
- Browsers without `safe center` fall back to plain `center` via the
  fallback-first double declaration; a wide table there could hide its left
  edge — modern evergreen browsers all support `safe`.

## Validation protocol (merge gate)

Before/after screenshots with identical fixtures are the acceptance
criterion for this change.

### Fixture DB (scratchpad, never committed)

Seed script: `bfeed migrate` against a throwaway `fixtures.db`, then
`sqlite3` inserts of 1 feed + 4 entries. Images are `data:image/svg+xml`
URIs with exact intrinsic dimensions (labeled colored rects) —
deterministic, offline, no third-party URLs. Render-time `proxifyImages`
leaves non-http(s) schemes alone and CSP `img-src` allows `data:`, so they
render without touching the SSRF-guarded proxy.

Entries:

1. **Images** — wide 1600x900 in `figure`+`figcaption`; small 320x200 figure
   (must NOT grow); portrait 800x1200; bare `<p><img></p>` 1200x675.
2. **Tables** — wide 12-column (bleeds + scrolls); narrow 2-column (stays
   centered at content width); prose between.
3. **Adversarial** — text + inline img paragraph (untouched); gallery
   `<p><img><img></p>` (excluded); 48x48 badge img in prose; nested
   `<div><figure>` (bleed works at depth).
4. **Prose control** — no media; before/after must differ only in header
   centering.

### Screenshot matrix

Viewports: 375x812 (phone, edge-to-edge case), 768x1024 (vw clamp binds),
960x900 (just above the 56rem=896px crossover, where clamp-arithmetic bugs
live), 1440x900 (wide tier binds).

1. **Before:** on `main`, serve with the fixture DB; Playwright fullPage
   screenshot per viewport x entry into `scratchpad/shots/before/`; on every
   page assert `document.documentElement.scrollWidth <= clientWidth`
   (deterministic horizontal-overflow check — classic-scrollbar bugs do not
   reproduce in headless overlay-scrollbar Chromium, so assert, don't
   pixel-hunt).
2. Implement on a branch.
3. **After:** identical script and fixtures into `after/`; re-run overflow
   assertions.
4. **Compare** against the invariant checklist: prose measure unchanged;
   small img / mixed-p / gallery rendering identical; wide img <=56rem,
   centered, symmetric; caption at measure; narrow table centered; wide
   table scrolls within slot; no horizontal overflow anywhere; prose-control
   entry differs only in header alignment.
5. One dark-theme spot check (images entry, 1440).
6. Any invariant miss: fix or invoke the documented fallback, then re-shoot
   the entire **after** set.

(Playwright MCP note: screenshots land in the repo root — collect before
committing.)

### PR

Curated subset (~8 PNGs: images + tables entries at 375 and 1440,
before/after) committed under `docs/img/pr-84/` on the branch and embedded
as a markdown table in the PR body; the full matrix stays in the scratchpad.

## Rollout

- Branch off `main`; CSS + template change + screenshot protocol.
- `CHANGELOG.md` entry under `[Unreleased]` / `Added` (user-facing).
- PR with screenshot table; request Copilot review via API per CLAUDE.md.
- No migrations, no config, no new routes.
