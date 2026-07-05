# Render-time promotion of image-only `<p>` to `<figure>` (#90)

**Issue:** [#90](https://github.com/bcrisp4/bfeed/issues/90) — the full-bleed
reader (#84/#88, v0.8.0) only widens `figure:has(img)` and
`div:has(> img:only-child)` slots, but the most common markup for article
images in feed/readability-extracted HTML is a bare paragraph wrapper
(`<p><img></p>`) or its WordPress click-to-enlarge variant
(`<p><a href><img></a></p>`). On that content the feature is inert: wide
charts render at the 34rem prose measure.

**Prior spec:** `2026-07-04-full-bleed-reader-design.md` (the CSS slot
system this builds on).

## Decision

Promote image-only paragraphs to `<figure>` at render time, in the web
layer, reader view only. The existing `figure:has(img)` CSS slot then
bleeds them — no new CSS heuristics, no stored-content mutation, no
backfill. Applies to all already-stored entries immediately; reversible by
deleting the transform.

## Why not CSS (recorded so it isn't re-litigated)

Two distinct CSS routes were assessed and both fail:

1. **Slot on the wrapper** (`p:has(> img:only-child)`): `:only-child`
   counts element children only, so `<p>text <img> text</p>` matches too,
   and the negative-margin slot bleeds paragraph *text* past the reading
   measure. Killed in #88 validation round 1. CSS selectors cannot see text
   nodes; there is no selector for "paragraph whose only content is an
   image".
2. **Style the img itself** (`p > img:only-child` as
   `display:block; position:relative; left:50%; transform:translateX(-50%)`
   plus a viewport-clamped `max-inline-size`): this *does* center an
   intrinsic-width image beyond its container (the issue's auto-margin
   objection doesn't apply to the transform trick), but `display:block`
   forces mid-text inline images — build badges, emoji in old blogs, which
   also match `:only-child` because text nodes don't count — onto their own
   line. A layout regression on real mixed-content paragraphs, and CSS
   cannot gate on intrinsic image width. Rejected.

Go's HTML walker sees text nodes, so the ambiguity that kills both CSS
routes does not exist server-side.

## Why not sanitize-time rewrite

Same detection at ingest would bake a presentation decision into stored
data and require a one-shot backfill for existing rows (cf. the 0013 FTS
backfill machinery). More moving parts for the same visible result. Revisit
only if render-time walking ever shows up in profiles (unlikely: the reader
already parses and re-renders the full entry DOM for the image proxy).

## Detection rule

Promote a `<p>` element when both hold:

- **Text:** every child text node is whitespace-only. Whitespace per
  `unicode.IsSpace`, which includes NBSP (U+00A0) — an NBSP-spacer
  paragraph (`<p><img>&nbsp;</p>`) still promotes.
- **Elements:** exactly one child element, which is either
  - an `<img>`, or
  - an `<a>` that itself passes the same test: whitespace-only text nodes
    and exactly one child element, an `<img>`.

Anything else — real text, a second `<img>`, `<br>`, `<em>`, `<span>`, an
`<a>` containing text alongside its image, an `<a>` with two images — means
no promotion. Non-goal: multi-image galleries (`<p><img><img></p>`) stay at
measure; feeds that mark galleries up as `<figure>` already bleed via the
existing slot (YAGNI — extend only if real feeds show the gap).

The sanitizer (bluemonday UGC policy + `img` attr allowlist,
`internal/sanitize`) keeps `p`, `a`, `img`, so both shapes survive to the
store; 1×1 tracking pixels are dropped before this transform ever sees
them.

## Transform

Rename the node in place: `Data = "figure"`, `DataAtom = atom.Figure`.
Attributes carried over unchanged. No wrapping, no reparenting.

The existing CSS then applies with **zero changes**:
`.reader .article figure{margin-inline:0}` +
`figure:has(img)` slot (negative `margin-inline`, `text-align:center`) +
`img{inline-size:auto; max-inline-size:100%}`. Consequence worth naming: a
*small* standalone image in a `<p>` becomes centered (not upscaled — 
`inline-size:auto` keeps intrinsic size), identical to how a real
`<figure>` renders today. Accepted; arguably an improvement.

## Placement / plumbing

`internal/web/proxify.go` (renamed to `transform.go` by this change) did
parse → per-node img-src rewrite → render, called only from the reader
handler (`handlers.go`, `(*Handler).entry`) and
gated behind `if h.imgRewrite != nil`. Refactor into a single
parse → transforms → render pipeline over one walk:

1. p→figure promotion (new) — runs **unconditionally**;
2. img-src proxy rewrite (existing) — runs only when a rewrite func is set.

The gate moves inside the pipeline: the handler always calls the transform,
`imgRewrite == nil` just skips pass 2. Reader-only placement deliberately
matches the CSS slots' `.reader` scope; list-row blurbs (`summaryText`)
strip tags anyway and never see this. `readingTime` keeps running on the
pre-transform content (element rename doesn't affect it, but the existing
ordering comment about proxy URLs stands).

## CSS comment update

The `app.css` comment "p:has(> img:only-child) deliberately NOT a slot"
gains one line: the Go render-time transform (`internal/web/transform.go`,
#90) promotes image-only `<p>` to `<figure>` instead, so bare-paragraph
images are covered without the text-bleed hazard.

## Testing

- **Unit (table-driven, `internal/web`):**
  - promote: `<p><img></p>`; whitespace text around img; NBSP text;
    `<p><a><img></a></p>`; nested `div > p > img` (walk reaches depth);
    attributes preserved on the renamed node; proxy rewrite still applied
    to an img inside a promoted figure.
  - no promotion: real text + img; `<p><img><br></p>`; two imgs;
    `<a>` with text + img; `<a>` with two imgs; `<p>` with no img;
    text-only `<p>`.
- **Screenshot validation (reuse #88 protocol):** fixture set already
  contains the exact cases. Flip the `entry-images.html` bare-`<p><img></p>`
  expectation from *not bled* to *bled*; `entry-adversarial.html` mixed
  text+img paragraph must **stay** unbled. Before/after at 375/768/960/1440
  with the `scrollWidth <= clientWidth` no-horizontal-overflow assertion.

## Changelog

`Fixed`: reader full-bleed now covers images wrapped in bare paragraphs and
paragraph-wrapped links, the most common feed markup shapes.
