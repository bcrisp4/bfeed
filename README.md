# bfeed

A free, self-hosted **RSS / Atom / JSON Feed reader** — a single pure-Go binary backed by one SQLite file, with a minimal, mobile-first, content-first htmx UI. Inspired by [Miniflux](https://miniflux.app/). Built to run comfortably on Raspberry Pi–class hardware for one to a handful of users.

- Subscribe with feed auto-discovery; organise feeds into categories
- Clean single-column reader — mobile-first, Light/Sepia/Dark themes, reading-time estimate, settings page
- Unread / Starred / History / per-feed / per-category views; full-text search (SQLite FTS5)
- Opt-in per-feed **full-content extraction** (readability) for feeds that only ship summaries
- Privacy by default: HTML sanitised before storage, trackers and tracking pixels stripped, images served through a signed same-origin proxy, strict CSP
- Polite **adaptive polling**: per-feed interval derived from publish rate, conditional GET (ETag / 304), per-host concurrency caps, exponential backoff, publisher TTL honoured

## Status: 🚧 work in progress

Daily-drivable as a single-user reader — the full subscribe → poll → read loop plus everything listed above. **No in-app login yet**: it is designed to sit behind a private network such as Tailscale.

Remaining work is tracked in [GitHub issues and milestones](https://github.com/bcrisp4/bfeed/milestones): authentication & multi-user, REST API + tokens, OPML import/export, retention cleanup, among others.

License: [Apache-2.0](LICENSE).

## Docs

- [`docs/design.md`](docs/design.md) — the full architecture & design (the long-term north star).
- [`docs/mvp-design.md`](docs/mvp-design.md) — the scope that is **actually built today**. When the code and `design.md` differ, this explains why.
- [GitHub issues & milestones](https://github.com/bcrisp4/bfeed/milestones) — the remaining work, one session-sized issue at a time. [`docs/roadmap.md`](docs/roadmap.md) keeps the tracking conventions and shipped history.
- [`docs/releasing.md`](docs/releasing.md) — how to cut a release (annotated semver tag → goreleaser).
- [`CHANGELOG.md`](CHANGELOG.md) — user-facing changes per release ([Keep a Changelog](https://keepachangelog.com/en/1.1.0/); policy in [`docs/changelog.md`](docs/changelog.md)).
- [`CLAUDE.md`](CLAUDE.md) — contributor/architecture notes (ports-and-adapters layout, invariants, sqlc workflow, test conventions).

## Build, test, run

Requires Go 1.25+. The build is pure Go (`CGO_ENABLED=0`, no cgo). Common tasks
go through the `Makefile`:

```bash
make build       # build ./cmd/bfeed (CGO_ENABLED=0)
make test        # unit tests
make test-race   # with the race detector — run before declaring anything done
make lint        # golangci-lint v2 (gofumpt/goimports, vet, staticcheck, gosec)
make fmt         # apply gofumpt/goimports
make run         # serve on :8080 (sets the required BFEED_BASE_URL for you)
make tools       # install pinned dev tools (golangci-lint, sqlc)
```

`make run` serves on http://localhost:8080 — open it and paste a feed URL
(e.g. https://hnrss.org/frontpage). Plain `go build` / `go test ./...` /
`go run ./cmd/bfeed serve` still work if you prefer them. `make help` is not
defined — run `make` with no target to lint+test+build (the `all` target).

> **Note:** `BFEED_BASE_URL` is the *external* URL (links/cookies/User-Agent) and is required. The *bind* address is `BFEED_LISTEN_ADDR` — they are separate.

### Subcommands

```
bfeed serve         run the HTTP server + background poller (default if omitted)
bfeed migrate       apply SQLite schema migrations (serve also auto-migrates on boot)
bfeed healthcheck   probe local /healthz, exit 0/1 (for container HEALTHCHECK)
bfeed version       print version / build info
```

### Container

A multi-stage **distroless** `Dockerfile` is included (non-root, static binary, `HEALTHCHECK` via `bfeed healthcheck`):

```bash
docker build -t bfeed:dev .      # or: make image  (tags bfeed:<git-describe>)
docker run --rm -e BFEED_BASE_URL=http://localhost:8080 -p 8080:8080 -v "$PWD/data:/data" bfeed:dev
```

Released multi-arch images are published to GHCR — `docker pull ghcr.io/bcrisp4/bfeed:<version>` (see [`docs/releasing.md`](docs/releasing.md)).

## Configuration

All configuration is via environment variables (12-factor), validated at startup.

| Variable | Default | Description |
|---|---|---|
| `BFEED_BASE_URL` | — (**required**) | External URL bfeed is reached at; used for absolute links, cookies, and the polling User-Agent. |
| `BFEED_LISTEN_ADDR` | `:8080` | Address the HTTP server binds to. |
| `BFEED_METRICS_ADDR` | — (disabled) | Optional separate bind address for a Prometheus `/metrics` endpoint (plus its own `/healthz`); leave unset to disable metrics entirely. |
| `BFEED_DATABASE_PATH` | `./bfeed.db` | Path to the SQLite database file (WAL/SHM files live alongside it). |
| `BFEED_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `BFEED_LOG_FORMAT` | `json` | `json` (prod) or `text` (dev). |
| `BFEED_POLL_TICK` | `1m` | How often the scheduler wakes to dispatch due feeds. |
| `BFEED_SCHED_MIN_INTERVAL` | `5m` | Floor on the adaptive per-feed poll interval (cold-start rate). |
| `BFEED_SCHED_MAX_INTERVAL` | `24h` | Ceiling on the adaptive per-feed poll interval. |
| `BFEED_SCHED_FACTOR` | `1.0` | Multiplier on publish frequency when computing a feed's adaptive interval (higher → polls more often). |
| `BFEED_FEED_ERROR_LIMIT` | `20` | Consecutive-error count after which the Feeds page marks a feed "stalled". |
| `BFEED_MAX_BACKOFF` | `24h` | Ceiling for exponential backoff on a feed that keeps erroring. |
| `BFEED_FEED_WORKERS` | `20` | Size of the background feed-poll worker pool. |
| `BFEED_BATCH_SIZE` | `100` | Max feeds dispatched per scheduler tick. |
| `BFEED_HOST_CONCURRENCY` | `3` | Max concurrent outbound requests per host (politeness). |
| `BFEED_SCRAPE_WORKERS` | `20` | Size of the full-content extraction worker pool. |
| `BFEED_SCRAPE_TICK` | `1m` | How often the scraper wakes to dispatch pending extractions. |
| `BFEED_SCRAPE_BATCH` | `50` | Max entries dispatched for extraction per scraper tick. |
| `BFEED_SCRAPE_MAX_ATTEMPTS` | `3` | Attempts before a failed full-content extraction is given up on. |
| `BFEED_IMAGE_PROXY` | `true` | Proxy remote images through `/img` (strips referrer, applies a strict CSP). |
| `BFEED_IMAGE_PROXY_SECRET` | — | Secret signing image-proxy URLs; if unset, a random key is generated once and persisted in the database. |
| `BFEED_BLOCK_PRIVATE_NETWORKS` | `true` | SSRF guard: refuse to fetch loopback/private/link-local addresses. |
| `BFEED_ALLOW_PRIVATE_CIDRS` | — | Comma-separated CIDRs exempted from the SSRF guard (e.g. a trusted internal feed host). |

Data lives entirely in the SQLite file at `BFEED_DATABASE_PATH` — back that up to back up everything.
