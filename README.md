# bfeed

A free, self-hosted **RSS / Atom / JSON Feed reader** — a single pure-Go binary backed by one SQLite file, with a minimal, mobile-first, content-first htmx UI. Inspired by [Miniflux](https://miniflux.app/). Built to run comfortably on Raspberry Pi–class hardware for one to a handful of users.

- Subscribe to feeds (with feed auto-discovery), read them in a clean single-column UI
- Polite background polling: conditional GET (ETag / 304), per-host concurrency caps, exponential backoff
- HTML sanitised before storage; trackers and tracking pixels stripped
- Mark read/unread, star, delete; keyset pagination

## Status: 🚧 work in progress (MVP)

This is **iteration 1 (MVP)**, on the `feat/mvp` branch. It implements the core loop — subscribe → poll → read → mark-read/star — as a **single user with no in-app login** (designed to sit behind a private network such as Tailscale).

Deliberately **not in the MVP yet** (tracked, with the path back, in [`docs/roadmap.md`](docs/roadmap.md)): authentication & multi-user, REST API + tokens, full-text search, categories, full-content scraping, image proxy, adaptive scheduling, OPML import/export, retention cleanup, and Prometheus metrics.

License: [Apache-2.0](LICENSE).

## Docs

- [`docs/design.md`](docs/design.md) — the full architecture & design (the long-term north star).
- [`docs/mvp-design.md`](docs/mvp-design.md) — the scope that is **actually built today**. When the code and `design.md` differ, this explains why.
- [`docs/roadmap.md`](docs/roadmap.md) — everything deferred past the MVP, and how each lands as an additive change.
- [`docs/releasing.md`](docs/releasing.md) — how to cut a release (annotated semver tag → goreleaser).
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
| `BFEED_DATABASE_PATH` | `./bfeed.db` | Path to the SQLite database file (WAL/SHM files live alongside it). |
| `BFEED_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `BFEED_LOG_FORMAT` | `json` | `json` (prod) or `text` (dev). |
| `BFEED_POLL_TICK` | `1m` | How often the scheduler wakes to dispatch due feeds. |
| `BFEED_POLL_INTERVAL` | `15m` | Fixed interval between polls of each feed. |
| `BFEED_MAX_BACKOFF` | `24h` | Ceiling for exponential backoff on a feed that keeps erroring. |
| `BFEED_FEED_WORKERS` | `20` | Size of the background feed-poll worker pool. |
| `BFEED_BATCH_SIZE` | `100` | Max feeds dispatched per scheduler tick. |
| `BFEED_HOST_CONCURRENCY` | `3` | Max concurrent outbound requests per host (politeness). |

Data lives entirely in the SQLite file at `BFEED_DATABASE_PATH` — back that up to back up everything.
