# Design: Makefile, CI & Release for bfeed

Status: **implemented** (branch `feat/ci-and-release`) · Date: 2026-06-19 · Author: Ben (with Claude)

This document is authoritative and describes what was **actually built**. Where
the implementation diverged from the original brainstorm (notably podman →
docker), the body reflects the as-built result; the divergences and why they
happened are recorded under [Changes during implementation](#changes-during-implementation).
For the day-to-day release procedure see `docs/releasing.md`.

## Goal

Give bfeed the same build/test/lint/release rigour as `pi5_exporter`, adapted
to bfeed's shape: a pure-Go (`CGO_ENABLED=0`, modernc SQLite) single binary with
**sqlc** codegen and everything (migrations, templates, static assets) embedded
via `go:embed`. End state: a `Makefile`, a CI workflow (test + lint + sqlc-sync),
and a tag-driven release workflow that ships multi-arch binaries and a multi-arch
GHCR container image with SBOMs and signed provenance.

## Settled decisions

| Decision | Choice |
|---|---|
| Pipeline scope | Full release (binaries + image + SBOM + attestation + changelog) |
| Target platforms | `linux/amd64` + `linux/arm64` (binaries and image) |
| Lint | golangci-lint **v2** (gofumpt + goimports), generated code excluded |
| Image build | **goreleaser owns images** via **`dockers_v2`** (Docker buildx) from prebuilt binaries |
| Container engine | **docker** everywhere — `make image` uses docker; CI builds multi-arch with docker buildx (native on `ubuntu-latest`) |
| Go toolchain | `toolchain go1.26.4` in `go.mod` (= current latest); dev Dockerfile base `golang:1.26` |
| gofumpt reformat | One-time, in **its own commit** (separate from feature commits) |
| License | **Apache-2.0** — `LICENSE` file + README link; archives ship it |
| Tool versions | Pinned to **latest available** as of 2026-06-19 (table below) |

Out of scope (YAGNI): Codecov upload (coverage → step summary only), darwin/windows
binaries, Homebrew tap, Cosign beyond GitHub attestation, hardware-test path.

## Tool & action versions (checked 2026-06-19 — latest available)

Pin everything; SHA-pin all GitHub Actions (tag is a comment, SHA is the anchor).

| Tool / action | Version | Pin (SHA for actions) |
|---|---|---|
| Go toolchain | `go1.26.4` | `go.mod` `toolchain` directive |
| golangci-lint | `v2.12.2` | action input `version: v2.12.2`; `make tools` installs it |
| sqlc | `v1.31.1` | `go install …/sqlc@v1.31.1` |
| goreleaser | `v2.16.x` | action input `version: "~> v2"` (tracks latest v2) |
| syft (SBOM) | `v1.45.1` | `sbom-action/download-syft` (fetches latest) |
| govulncheck | latest | `go run …/govulncheck@latest` |
| actions/checkout | `v7.0.0` | `9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0` |
| actions/setup-go | `v6.4.0` | `4a3601121dd01d1626a1e23e37211e3254c1c06c` |
| golangci/golangci-lint-action | `v9.2.1` | `82606bf257cbaff209d206a39f5134f0cfbfd2ee` |
| goreleaser/goreleaser-action | `v7.2.2` | `5daf1e915a5f0af01ddbcd89a43b8061ff4f1a89` |
| anchore/sbom-action | `v0.24.0` | `e22c389904149dbc22b58101806040fa8d37a610` |
| docker/setup-qemu-action | `v4.1.0` | `06116385d9baf250c9f4dcb4858b16962ea869c3` |
| docker/setup-buildx-action | `v4.1.0` | `d7f5e7f509e45cec5c76c4d5afdd7de93d0b3df5` |
| actions/attest-build-provenance | `v4.1.0` | `a2bbfa25375fe432b6a289bc6b6cd05ecd0c4c32` |

`setup-qemu-action` registers QEMU binfmt handlers (arm64 emulation);
`setup-buildx-action` provisions the `docker-container`-driver builder that
`dockers_v2` needs to build a multi-arch manifest. GHCR auth is an explicit
`docker login` step (no `docker/login-action`).

## Why this differs from pi5_exporter

bfeed is **not** a copy of pi5's config. Deltas:

1. **Version stamping** is `-X main.version=...` (bfeed has a single `main.version`
   var in `cmd/bfeed/main.go`), **not** `prometheus/common/version`.
2. **sqlc-sync check** is a bfeed-specific CI job. pi5 has no codegen.
3. **No `pi5_hardware` build tag** anywhere.
4. **Generated code excluded from lint/format**: `internal/store/sqlite/sqlc/`,
   committed-but-never-hand-edited.
5. **Cross-compile is trivial** (pure Go) — arm64 builds natively on the amd64
   runner; no QEMU compile. The COPY-only release image means QEMU (for the arm64
   image) only runs a `COPY` + metadata, never a `go build`.
6. **goreleaser owns the images** via `dockers_v2` (pi5 used a separate
   `build-push-action` job compiling from source). One tool stamps the version
   into binaries and images and emits `digests.txt` for image attestation.

## Deliverables

### 1. `Makefile`

bfeed-flavoured. Key points:

- `VERSION ?= git describe --tags --always --dirty`; `LDFLAGS := -s -w -X main.version=$(VERSION)`.
- `build` (+ `build-linux-amd64`/`-arm64` cross), `test`, `test-race`, `vet`, `tidy`.
- `lint` / `fmt` invoke golangci-lint resolved as
  `$(shell command -v golangci-lint || echo $(GOPATH)/bin/golangci-lint)` — so it
  works whether or not `GOPATH/bin` is on `PATH`. Same config CI uses.
- `tools` installs the pinned dev tools (`golangci-lint@v2.12.2`, `sqlc@v1.31.1`).
- bfeed-specific: `sqlc` (regen), `sqlc-check` (`sqlc generate && git diff --exit-code …/sqlc`),
  `migrate` (sets `BFEED_BASE_URL` — the config loader requires it even though
  migration doesn't use it), `run` (sets the required `BFEED_BASE_URL`).
- `image`: **`docker build -t bfeed:$(VERSION) .`** against the multi-stage dev
  `Dockerfile` (host arch, local dev). The multi-arch release image is
  goreleaser's job.
- `clean`. `.PHONY` on all targets.

### 2. `.golangci.yml`

golangci-lint **v2**. Linters: standard + bodyclose, copyloopvar, errorlint,
gocheckcompilerdirectives, gosec, misspell, nilerr, revive, unconvert, usetesting;
revive rule set per pi5. Formatters: gofumpt (`extra-rules: true`) + goimports
(`local-prefixes: github.com/bcrisp4/bfeed`). Exclusions: `_test.go` relaxes
gosec/errcheck; generated `internal/store/sqlite/sqlc/` is excluded from **both**
linters and formatters (committed, never hand-edited, must not fight regeneration).

### 3. `.github/workflows/ci.yml`

Triggers `push: [main]` + `pull_request`; `concurrency` cancel-in-progress;
`permissions: {}` top-level, each job adds `contents: read`; all actions
SHA-pinned; every checkout `persist-credentials: false`; every setup-go uses
`go-version-file: go.mod` + `check-latest: true` + `cache-dependency-path: go.sum`.

- **test**: `go test -race -shuffle=on -timeout=120s -covermode=atomic
  -coverprofile=coverage.out ./...`; coverage one-liner → `$GITHUB_STEP_SUMMARY`;
  arm64 cross-compile to `/dev/null`.
- **lint**: golangci-lint-action `version: v2.12.2` + `govulncheck`
  (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`).
- **sqlc-sync**: install `sqlc@v1.31.1`, `sqlc generate`,
  `git diff --exit-code internal/store/sqlite/sqlc` — fails on stale generated code.

### 4. `.github/workflows/release.yml`

Trigger: semver tags `v[0-9]+.[0-9]+.[0-9]+` (+ `-*` prereleases); `permissions: {}`
top-level. Single `goreleaser` job (`contents/packages/id-token/attestations: write`):

- checkout `fetch-depth: 0` + `persist-credentials: false`; setup-go;
  **`docker/setup-qemu-action`** (arm64 binfmt) + **`docker/setup-buildx-action`**
  (multi-arch builder — `dockers_v2` cannot build a manifest on the runner's
  default builder); `sbom-action/download-syft`;
  **`docker login ghcr.io`** via `--password-stdin` with `GITHUB_TOKEN`;
  goreleaser-action `version: "~> v2"`, `args: release --clean`.
- **Attestation** (`actions/attest-build-provenance`):
  - binaries — `subject-checksums: dist/checksums.txt` (firm);
  - images — `subject-checksums: dist/digests.txt`, the file goreleaser's
    `docker_digest` writes (firm; the documented goreleaser+attest pattern).

### 5. `.goreleaser.yaml`

v2:

- **builds**: `main: ./cmd/bfeed`, `binary: bfeed`, `env: [CGO_ENABLED=0]`,
  `flags: [-trimpath]`, `mod_timestamp: "{{ .CommitTimestamp }}"`, `goos: [linux]`,
  `goarch: [amd64, arm64]`, `ldflags: ["-s -w -X main.version={{ .Version }}"]`.
- **archives**: `tar.gz`, name `{{.ProjectName}}_{{.Version}}_{{.Os}}_{{.Arch}}`,
  `files: [LICENSE, README.md]`.
- **checksum**: sha256 `checksums.txt`. **sboms**: `artifacts: archive`.
- **docker_digest**: `name_template: "digests.txt"` — writes `dist/digests.txt`
  (image@sha256 lines) so the workflow attests images from a reliable source.
- **dockers_v2** (one entry, `id: bfeed`): `dockerfile: Dockerfile.release`,
  `platforms: [linux/amd64, linux/arm64]`, `images: [ghcr.io/bcrisp4/bfeed]`,
  `tags: ["{{.Version}}", "{{ if not .Prerelease }}{{.Major}}.{{.Minor}}{{end}}",
  "{{ if not .Prerelease }}latest{{end}}"]` (floating tags render empty on
  prereleases and goreleaser drops empty tags), OCI `labels`
  (`image.version/revision/source`).
- **changelog**: `use: github`, exclude `^docs:`/`^test:`/`^chore:`/`^ci:`.
- **release**: `github: {owner: bcrisp4, name: bfeed}`, `prerelease: auto`.

### 6. `Dockerfile.release`

COPY-only distroless — goreleaser drops per-platform binaries into the build
context under `$TARGETPLATFORM/`, so **no build stage**:

```dockerfile
FROM gcr.io/distroless/static:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/bfeed /bfeed
ENV BFEED_DATABASE_PATH=/data/bfeed.db
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s CMD ["/bfeed", "healthcheck"]
ENTRYPOINT ["/bfeed"]
CMD ["serve"]
```

The existing multi-stage `Dockerfile` (builds from source) stays for local
`make image` (docker) dev. This works because the binary is fully self-contained
(migrations, templates, `static/` all `go:embed`-ed).

### 7. Repo edits

- **`go.mod`**: `toolchain go1.26.4`.
- **`LICENSE`** (Apache-2.0 full text) + **`README.md`** license line → Apache-2.0
  link, plus a `docs/releasing.md` pointer in the Docs list.
- **`CLAUDE.md`**: Commands section points at the Makefile targets and names
  golangci-lint v2 as the lint bar; links `docs/releasing.md`.
- **`docs/releasing.md`** (new): annotated semver tag → goreleaser, what the
  pipeline produces, verify (`gh release view`, `docker pull`,
  `gh attestation verify`), prerelease behaviour, local `docker buildx` dry-run,
  and rollback guidance.
- **One-time gofumpt/goimports reformat** of existing `.go` files — its own commit.
- **Lint-finding fixes** (see below) — its own commit.

## Changes during implementation

Recorded for history; the body above is the authoritative as-built state.

- **podman → docker (engine pivot).** The brainstorm chose podman (`use: podman`
  in goreleaser, podman everywhere). During implementation we found **goreleaser
  v2.16 (latest) removed `use: podman`** — only `buildx`/`docker` are valid, and
  the `dockers`/`docker_manifests` pipes are deprecated in favour of **`dockers_v2`**
  (buildx-only). Given that, the user chose **docker everywhere**. Net effect:
  `dockers_v2` + Docker buildx in CI (with `setup-buildx-action`), `docker login`,
  `docker_digest` for image attestation, `Dockerfile.release` uses
  `ARG TARGETPLATFORM`, and `make image` shells out to `docker`. Validated with
  `goreleaser release --snapshot --clean` (both-arch images build).
- **`golang.org/x/net` v0.53.0 → v0.56.0.** The new `govulncheck` CI job flagged
  three reachable vulns (GO-2026-5028/5029/5030, html parsing, reachable via
  `parse.Parser.Discover → html.Parse`); bumped past the v0.55.0 fix.
- **15 golangci-lint findings on existing code** resolved in one commit — real
  fixes (healthcheck response-body leak + nil-deref, `ReadHeaderTimeout`,
  `%w` wrapping, blank-import doc, checked `Close`/`Rollback`,
  `http.StatusUnprocessableEntity`) and justified `//nolint:gosec` suppressions
  (jitter RNG, drained body, parameterised SQL with allowlisted identifiers).
- **Two release-pipeline bugs found by validation** (not config-check):
  `{{ .IsPrerelease }}` (not a goreleaser field) → `{{ .Prerelease }}`; and image
  attestation switched from scraping `artifacts.json` to `docker_digest`/`digests.txt`.
- **Makefile robustness fixes**: golangci-lint path resolution (PATH or GOPATH/bin),
  `migrate` sets `BFEED_BASE_URL`, added `make tools`.

## Commit sequence (as built, branch `feat/ci-and-release`)

1. `build: add toolchain go1.26.4 and Apache-2.0 license`
2. `build: add Makefile and golangci-lint config`
3. `chore: gofumpt/goimports reformat` *(isolated, formatting-only)*
4. `fix: resolve golangci-lint findings`
5. `ci: add CI workflow (test, lint, sqlc-sync)`
6. `fix(deps): bump golang.org/x/net to v0.56.0`
7. `ci: add release pipeline (goreleaser binaries + GHCR image)`
8. `fix(ci): correct goreleaser prerelease tag template; docker for local image`
9. `fix(ci): attest manifest-list digest, not per-arch image digest`
10. `docs: add release process guide`
11. `docs: update build/lint commands and link release guide in CLAUDE.md`
12. `docs: amend CI/release spec` → superseded by this authoritative rewrite
13. `fix(ci): provision buildx builder and attest images via docker_digest`
14. `fix(make): resolve golangci-lint path, fix migrate, add tools target`

## Verification (as built)

- `make build` (cgo-free), `go test ./... -race`, `make lint` (0 issues),
  `make sqlc-check` (no drift), `make migrate`, and the other Makefile targets all
  pass; `make tools` installs the pinned tools.
- `goreleaser check` passes; `goreleaser release --snapshot --clean` builds both
  arch images via buildx and writes archives + `checksums.txt` + per-archive SBOMs.
- **CI green on PR #1** — `test`, `lint` (+govulncheck), `sqlc-sync`.

## Open items / risks

- **Live release path unproven until a real tag.** GHCR push, multi-arch manifest
  creation, `digests.txt`, and image attestation only run on a tag push (not in CI
  or `--snapshot`). The config and the buildx-builder requirement are validated;
  cutting a throwaway `vX.Y.Z-rc1` prerelease (rollback documented in
  `docs/releasing.md`) is the recommended end-to-end confirmation.
- **Floating-version tools** (`govulncheck@latest`, syft "latest") can shift CI
  behaviour under you — accepted for a single-user project (it's what surfaced the
  `x/net` CVEs).
- **sqlc pin (`v1.31.1`)** has no in-file version stamp to confirm against; the
  sqlc-sync job is the real check (green on PR #1).
