# Image proxy & SSRF-guarded fetch — design (iteration 5)

> Status: approved design, ready for implementation planning.
> Scope: privacy iteration. Route entry images through a signed same-origin proxy so the
> reader's browser never contacts origin/tracker servers, and harden all outbound fetches
> against SSRF.
> North star: `docs/design.md` §10.5, §10.6, §17, §27 (invariants 3, 5). Backlog: `docs/roadmap.md` A4.

---

## 1. Purpose

Without the proxy, every `<img>` in an entry is fetched directly by the reader's browser on
render, leaking the reader's IP and User-Agent to origin servers and any third-party tracker
hosts the image points at. This iteration makes images load through bfeed: the browser only
ever talks to bfeed, and bfeed fetches images server-side through the polite, SSRF-guarded
HTTP client.

It also closes a smaller hole: feed-content and article-scrape fetches currently use the
default HTTP transport with no protection against a feed (or a redirect) pointing at a
private/loopback/metadata address. The SSRF guard added here covers all outbound fetches.

## 2. Decisions (locked during brainstorming)

| Decision | Choice | Rationale |
|---|---|---|
| Server-side image cache | **Deferred** — browser cache only | Roadmap A4 lists the cache as a separate line. `/img` serves a one-year `immutable` `Cache-Control`, so the browser caches per device. Single-user tailnet ⇒ browser cache covers repeat views. Smallest correct increment. |
| SSRF scope | **Global default-deny** on the one shared Fetcher + CIDR allowlist escape hatch | User has no private-network feeds today. Guarding all outbound is the secure reading, costs nothing now, keeps the single shared per-host budget (invariant 5), and matches design §10.6 "through the polite Fetcher". |
| Responsive images (`srcset`) | **src-only** — keep stripping `srcset` | bluemonday already strips `srcset` (not in the allowlist), so there is no leak today. A single-column reader does not need responsive variants. YAGNI. |
| Default state | **ON** (`BFEED_IMAGE_PROXY=on`) | Design §10.6. The privacy win the feature exists for. |
| Where images are rewritten | **Render-time** (web layer), not ingest (sanitize) | List views are text-only (`summaryText` strips all tags), so only the single reader view renders `<img>` — render-time costs one article parse per reader page-view. In exchange: legacy entries are also proxied, secret rotation is harmless, toggling the proxy off is instant and clean, and stored content stays canonical (origin URLs). |

### Why render-time, in detail

The alternative (design §10.5) is to rewrite `<img src>` to a signed `/img` URL inside the
sanitizer, so stored entry HTML already contains proxy URLs. That was rejected here:

- **Legacy coverage.** Entries already in the DB were sanitised before this feature; their
  stored HTML has origin `src`. Ingest-time rewrite only affects *future* polls/scrapes, so
  old entries keep leaking. Render-time proxies every entry regardless of age.
- **Rotation.** With proxy URLs baked into stored content, rotating the HMAC secret breaks
  every stored entry until it is re-polled. Render-time re-signs on each render, so rotation
  is harmless (design §10.6's stated expectation).
- **Toggle safety.** If proxy URLs are stored and the operator later disables the proxy, the
  `/img` route disappears and every stored entry shows broken images. Render-time keeps stored
  URLs canonical (origin), so on/off is a pure presentation switch.
- **Cost is negligible.** The list/summary path (`internal/web/summary.go`) converts HTML to
  plain text, so no list row renders an image. Only `entry.gohtml` renders `{{.Entry.Content}}`
  as HTML. Render-time rewrite therefore parses exactly one article per reader page-view.

Consequence: the sanitizer is **not** modified by this iteration. This is a deliberate delta
from design §10.5 and is recorded in the design decision log.

## 3. Components

Dependencies still point inward (design §5). `internal/core` is untouched except for one new
consumer-owned port (`SettingStore`). Adapters change; `cmd/bfeed` wires them.

### 3.1 `internal/fetch` (modify) — SSRF guard

The single shared `*fetch.Client` (used by feed polling, article scraping, and the image
proxy) gains private-network blocking.

- `fetch.Config` gains:
  - `BlockPrivateNetworks bool`
  - `AllowedCIDRs []netip.Prefix`
- The client builds an explicit `*http.Transport` whose `DialContext` comes from a
  `net.Dialer` with a `Control` hook. `Control(network, address, conn)` runs **after DNS
  resolution, once per dialled address** (so it also covers addresses reached via redirects,
  and there is no resolve-then-connect TOCTOU / DNS-rebind window). It parses the `ip:port`
  `address` and:
  1. if the IP is inside any `AllowedCIDRs` prefix → permit;
  2. else if the IP is in the block set → return an error (dial fails);
  3. else → permit.
- **Block set** (when `BlockPrivateNetworks` is true): an IP is blocked when any of
  `netip.Addr` `IsLoopback`, `IsPrivate` (RFC1918 + ULA `fc00::/7`), `IsLinkLocalUnicast`
  (covers `169.254.0.0/16`, incl. the `169.254.169.254` metadata address, and `fe80::/10`),
  `IsLinkLocalMulticast`, `IsMulticast`, `IsInterfaceLocalMulticast`, `IsUnspecified` hold, or
  the IP is in CGNAT `100.64.0.0/10` (checked explicitly — `IsPrivate` does not include it).
  Map IPv4-in-IPv6 to v4 (`Unmap`) before checking.
- When `BlockPrivateNetworks` is false, `Control` permits everything (current behaviour).
- A blocked dial surfaces as a normal `Fetch` error (wrapped), which callers already handle:
  the poller records it as a feed error; the image proxy maps it to 502.

Existing per-host concurrency, redirect cap, timeout, and max-bytes behaviour are unchanged.

### 3.2 `internal/imgproxy` (new)

A small package with three pieces.

**Signer** — keyed by the resolved HMAC secret (`[]byte`):
- `Sign(rawURL string) string` → lowercase hex of `HMAC-SHA256(secret, rawURL)`.
- `Verify(rawURL, sig string) bool` → recompute and compare with `hmac.Equal` (constant time).
- `ProxyURL(rawURL string) string` → `"/img?u=" + url.QueryEscape(rawURL) + "&s=" + Sign(rawURL)`.
  Relative (same-origin); the browser resolves it against the page origin.

**Handler** — `http.Handler` for `GET /img`:
1. Read `u` and `s` query params. Missing either → `400`.
2. `Verify(u, s)` → false → `403` (never an open relay: only URLs bfeed signed are proxied).
3. Parse `u`; scheme must be `http` or `https` → else `400`.
4. Fetch through the shared `core.Fetcher` (empty ETag/Last-Modified — no conditional GET).
   - dial/transport error (incl. an SSRF-blocked dial) → `502`.
   - response status ≠ 200 → `502`.
   - response `Content-Type` does not start with `image/` → `502` (never serve non-image
     bytes as an image — defends against an origin returning HTML/text).
5. Success: write the origin `Content-Type`, `Cache-Control: public, max-age=31536000,
   immutable`, `X-Content-Type-Options: nosniff`, and the (already byte-capped) body.

SSRF is enforced once, at the fetch dial layer — the handler does not re-resolve the host.
Errors are logged (slog); no Prometheus (observability is deferred, roadmap A12).

**Secret resolution** — `ResolveSecret(ctx, store SettingStore, envSecret string) ([]byte, error)`:
- `envSecret != ""` → use its raw bytes as the key (operator-managed; document that a long
  random value is recommended).
- else `GetSetting(ctx, "image_proxy_secret")`:
  - found → hex-decode to the key.
  - not found → generate 32 bytes via `crypto/rand`, hex-encode, `PutSetting` it, use it.
- The key lives in the same SQLite volume as the data, so it is stable across restarts.

### 3.3 `app_settings` table (new) — migration 0006

```sql
-- +goose Up
CREATE TABLE app_settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE app_settings;
```

- New static queries in `internal/store/sqlite/queries/settings.sql`, compiled by sqlc:
  - `GetAppSetting :one` — `SELECT value FROM app_settings WHERE key = ?`.
  - `PutAppSetting :exec` — `INSERT INTO app_settings (key, value) VALUES (?, ?)
    ON CONFLICT(key) DO UPDATE SET value = excluded.value`.
- Regenerate sqlc (`make sqlc`) and commit the generated code.
- New consumer-owned port in `internal/core/ports.go`:
  ```go
  type SettingStore interface {
      GetSetting(ctx context.Context, key string) (string, error) // ErrNotFound when absent
      PutSetting(ctx context.Context, key, value string) error
  }
  ```
  Added to the composed `Store` interface. `GetSetting` returns `core.ErrNotFound` when the
  row is missing (so `ResolveSecret` can branch on it).
- `internal/store/sqlite` implements the two methods (mapping `sql.ErrNoRows` → `ErrNotFound`).
- `internal/core/coretest.MemStore` gains an in-memory `map[string]string` implementation so
  core/web tests can exercise secret resolution.

### 3.4 `internal/web` (modify) — render-time rewrite + mount `/img`

- `web.New` gains two optional hooks, both nil when the proxy is disabled:
  - `imgHandler http.Handler` — mounted at `GET /img` when non-nil.
  - `imgRewrite func(rawURL string) string` — applied to reader content when non-nil.
- web takes **functions/handlers, not imgproxy types**, so it stays decoupled from the
  imgproxy package (web imports only `core` today; that holds).
- Reader view model construction: when `imgRewrite != nil`, parse the entry `Content`
  (`golang.org/x/net/html`), and for each `<img>` rewrite `src` to `imgRewrite(absSrc)` where
  `absSrc` is the already-absolute stored URL; re-render to a string and wrap as
  `template.HTML`. The content is already sanitised, so this pass only swaps attribute values
  — it never re-introduces unsafe markup and bluemonday is not re-run.
  - Only `http`/`https` `src` values are rewritten. `data:` URIs (some feeds inline base64
    images), empty, and other-scheme `src` are left untouched — they leak nothing and must
    not be signed/fetched.
  - A small helper, e.g. `proxifyImages(html string, rewrite func(string) string) string`,
    lives in the web package alongside the existing humanise/summary helpers.
- The `/img` handler sets its own `Cache-Control`, which overrides the `noStore` middleware
  (headers are not flushed until the handler writes). The compression allowlist already skips
  `image/*` (only `image/svg+xml` is compressible), so proxied images pass through uncompressed.
- List/summary rendering is untouched (text-only; no images).

### 3.5 `internal/config` (modify)

New environment variables, validated at startup:

| Env | Type | Default | Notes |
|---|---|---|---|
| `BFEED_IMAGE_PROXY` | bool (`on/off/true/false/1/0`) | **on** | Master switch for the proxy + render-time rewrite. |
| `BFEED_IMAGE_PROXY_SECRET` | string | "" | Optional operator-managed HMAC key; generated + persisted if empty. |
| `BFEED_BLOCK_PRIVATE_NETWORKS` | bool | **on** | SSRF guard on all outbound fetches. |
| `BFEED_ALLOW_PRIVATE_CIDRS` | comma-separated CIDRs | "" | Re-permit specific blocked ranges (e.g. `100.64.0.0/10` for a tailnet feed, `192.168.0.0/16` for a LAN feed). Invalid CIDR → startup error. |

Deferred (not added this iteration): `BFEED_IMAGE_CACHE_DIR`, `BFEED_IMAGE_CACHE_MAX_BYTES`
(cache deferred), `BFEED_IMAGE_MAX_BYTES` (image fetches reuse the shared 10 MiB body cap).

A bool env parser (`on/off/true/false/1/0`, default-aware) is added to `config` alongside the
existing `envInt`/`envDur` helpers.

### 3.6 `cmd/bfeed/serve.go` (wire)

- Build the fetcher with `BlockPrivateNetworks: cfg.BlockPrivateNetworks` and
  `AllowedCIDRs: cfg.AllowPrivateCIDRs`.
- If `cfg.ImageProxy`:
  - `secret := imgproxy.ResolveSecret(ctx, store, cfg.ImageProxySecret)` (fatal on error);
  - `signer := imgproxy.NewSigner(secret)`;
  - `imgHandler := imgproxy.New(fetcher, signer, log)`;
  - `imgRewrite := signer.ProxyURL`.
  - else both nil.
- `sanitize.New()` is called unchanged.
- `web.New(feedSvc, entrySvc, catSvc, searchSvc, log, imgHandler, imgRewrite)`.

## 4. Data flow

**Render (proxy on):** browser requests an entry → `GET /entries/{id}` → handler builds the
reader VM → `proxifyImages` rewrites each `<img src>` to `/img?u=…&s=…` → page returned. The
browser then issues `GET /img?u=…&s=…` per image → handler verifies the signature → fetches
the origin image through the SSRF-guarded shared Fetcher → streams it back with a long cache
header. The browser never contacts origin directly.

**Render (proxy off):** reader VM keeps origin `src`; no `/img` route; current behaviour.

**Secret (first run, no env):** startup generates 32 random bytes, hex-encodes, persists to
`app_settings[image_proxy_secret]`, and uses it for signing/verifying for the process life and
all future restarts.

## 5. Error handling

- `imgproxy` handler: `400` (missing params / bad scheme), `403` (bad signature), `502`
  (fetch/SSRF/non-200/non-image). Never echoes an origin error body. Always sends `nosniff`.
- `fetch` SSRF block: returned as a wrapped `Fetch` error; the poller records a feed error,
  the scraper backs off, the image proxy returns 502 — all existing paths.
- Secret resolution DB failure at startup: fatal (loud misconfiguration beats silently
  serving broken/unsafe images).
- Background image fetch failures are logged; no metrics this iteration.

## 6. Invariants defended (design §27)

- **3 (image proxy is never an open relay):** only HMAC-signed URLs are served; private/
  loopback/link-local/metadata IPs are rejected at dial; an `image/*` content-type is required.
- **5 (one per-host budget):** the image proxy uses the same shared Fetcher and its per-host
  semaphore as feed polls and scrapes.
- **1/2 (sanitised HTML):** unchanged — content is sanitised at ingest as before; the
  render-time rewrite only swaps `img src` values on already-safe HTML and never re-runs a
  weaker policy.
- **19–21 (architecture/time):** core imports no adapter; the only core change is the new
  consumer-owned `SettingStore` port; no `time.Now()` added to core.

## 7. Testing (TDD)

- **fetch (integration):** an `httptest.Server` listens on `127.0.0.1`; a `Fetch` with
  `BlockPrivateNetworks` is **rejected** at dial; with `127.0.0.0/8` in `AllowedCIDRs` it
  **succeeds**; a public-IP dial path is permitted. Cover IPv4 and `::1`.
- **imgproxy:** sign/verify roundtrip; tampered signature → 403; missing `u`/`s` → 400;
  non-`http(s)` scheme → 400; an origin returning `text/html` → 502; a happy-path image (via a
  stub/`httptest` origin) → 200 with the right `Content-Type` + cache headers.
- **secret resolution:** generate-once — two `ResolveSecret` calls against the same store
  return identical keys; an env secret overrides the store.
- **web:** with `imgRewrite` set, the reader response rewrites `<img src>` to `/img?u=…&s=…`
  and the embedded signature verifies; with it nil, `src` is the unchanged origin URL; list
  views are unaffected either way.
- **store/sqlite:** `app_settings` get/put roundtrip; `GetSetting` of a missing key →
  `ErrNotFound`; `PutAppSetting` upsert overwrites.
- **config:** `BFEED_IMAGE_PROXY` / `BFEED_BLOCK_PRIVATE_NETWORKS` bool parsing; CIDR-list
  parse; an invalid CIDR returns a startup error.

## 8. Documentation & changelog

- `docs/design.md`: add an iteration-5 decision-log entry recording the render-time rewrite
  delta from §10.5, the global SSRF default-deny + allowlist, and the deferred cache.
- `docs/mvp-design.md`: note the image proxy is now built.
- `docs/roadmap.md`: move the A4 rows that ship (signed endpoint, SSRF guard, secret
  resolution, render-time rewrite) to Done (iter 5); leave the image **cache** row deferred.
- `CHANGELOG.md` `[Unreleased]`: `Added` the image proxy (default on); `Security` the
  private-network (SSRF) block on all outbound fetches with the CIDR allowlist.

## 9. Out of scope (deferred)

- Server-side image cache (on-disk or LRU) — roadmap A4, its own line.
- `srcset` / responsive-image rewriting.
- Per-image byte cap distinct from the shared fetch cap.
- Prometheus metrics for the image proxy (`bfeed_errors_total{component=image_proxy}`) —
  roadmap A12.
- A failure placeholder image (broken-image icon is acceptable).
- Secret rotation tooling (rotation already works by changing the env / clearing the setting;
  render-time signing makes it harmless without re-polling).
