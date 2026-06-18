# Design: Makefile, CI & Release for bfeed

Status: approved (brainstorm) · Date: 2026-06-19 · Author: Ben (with Claude)

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
| Lint | golangci-lint **v2**, mirror pi5 (gofumpt + goimports), generated code excluded |
| Image build | **goreleaser owns images** (`dockers` + `docker_manifests`) from prebuilt binaries |
| Go toolchain | Add `toolchain go1.26.4` to `go.mod`; Dockerfile base already `golang:1.26` |
| gofumpt reformat | One-time, in **its own commit** (separate from feature commits) |
| License | **Apache-2.0** — `LICENSE` file + README update; archives ship it |

Out of scope (YAGNI): Codecov upload (coverage → step summary only), darwin/windows
binaries, Homebrew tap, Cosign beyond GitHub attestation, hardware-test path.

## Why this differs from pi5_exporter

bfeed is **not** a copy of pi5's config. Deltas:

1. **Version stamping** is `-X main.version=...` (bfeed has a single `main.version`
   var in `cmd/bfeed/main.go`), **not** `prometheus/common/version`. No `Revision`/
   `Branch`/`BuildDate` ldflags — just `main.version`.
2. **sqlc-sync check** is a new, bfeed-specific CI job. pi5 has no codegen.
3. **No `pi5_hardware` build tag** anywhere (golangci `build-tags`, `test-hw` target).
4. **Generated code is excluded from lint**: `internal/store/sqlite/sqlc/` and the
   goose migrations are committed-but-never-hand-edited.
5. **Cross-compile is trivial** (pure Go) — arm64 builds natively on the amd64
   runner; no QEMU compile. The COPY-only release image means QEMU (for the arm64
   image's `docker build`) only runs a `COPY` + metadata, never a `go build`.
6. **goreleaser builds the images** (pi5 used a separate `build-push-action` job
   compiling from source). One tool stamps the version into binaries and images.

## Deliverables

### 1. `Makefile`

bfeed-flavoured, pi5 shape. Targets:

```
BINARY  := bfeed
PKG     := github.com/bcrisp4/bfeed
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

all: lint test build

build:              CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bfeed
build-linux-amd64: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bfeed
build-linux-arm64: CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bfeed

test:       go test ./...
test-race:  go test -race ./...

lint:       golangci-lint run          # single source of truth shared with CI
fmt:        golangci-lint fmt          # apply gofumpt/goimports
vet:        go vet ./...
tidy:       go mod tidy

# bfeed-specific
sqlc:       sqlc generate
sqlc-check: sqlc generate && git diff --exit-code internal/store/sqlite/sqlc
migrate:    go run ./cmd/bfeed migrate
run:        # build + run with required env
            BFEED_LISTEN_ADDR=:8080 BFEED_BASE_URL=http://localhost:8080 BFEED_LOG_FORMAT=text \
              go run ./cmd/bfeed serve

clean:      rm -f $(BINARY)
```

`.PHONY` on all. `lint`/`fmt` delegate to golangci-lint so the Makefile and CI
never drift. `run` bakes in the **required** `BFEED_BASE_URL` (per CLAUDE.md it has
no default and is mandatory).

### 2. `.golangci.yml`

Copy pi5's v2 config with these edits:
- Remove the `run.build-tags: [pi5_hardware]` block.
- Set `formatters.settings.goimports.local-prefixes` to `github.com/bcrisp4/bfeed`.
- **Exclude generated code** under `linters.exclusions.rules` and
  `formatters.exclusions` (golangci v2): paths `internal/store/sqlite/sqlc/` and
  `internal/store/sqlite/migrations/`. (sqlc output is committed and never
  hand-edited; linting/formatting it is noise and could fight regeneration.)
- Keep the linter set: standard + bodyclose, copyloopvar, errorlint,
  gocheckcompilerdirectives, gosec, misspell, nilerr, revive, unconvert,
  usetesting. Keep the `_test.go` relaxations (gosec, errcheck).
- Keep formatters gofumpt (`extra-rules: true`) + goimports.

### 3. `.github/workflows/ci.yml`

Triggers: `push: [main]`, `pull_request`. `concurrency` cancel-in-progress.
`permissions: {}` top-level; each job opts in to `contents: read`. **All actions
SHA-pinned** with version comments (reuse pi5's pins for checkout/setup-go/
golangci-lint-action). `setup-go` uses `go-version-file: go.mod` + `check-latest`
+ `cache-dependency-path: go.sum`.

Jobs:

- **test**: `go test -race -shuffle=on -timeout=120s -covermode=atomic
  -coverprofile=coverage.out ./...`; coverage summary (`go tool cover -func | tail
  -1`) → `$GITHUB_STEP_SUMMARY`; then cross-compile arm64 to `/dev/null`
  (`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/bfeed`).
- **lint**: golangci-lint-action `version: v2.12` (match pi5) + `govulncheck`
  (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`).
- **sqlc** (bfeed-specific): install pinned sqlc
  (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1` — the version currently
  installed locally; the generated header carries no version stamp, so the sync
  check below *is* the validation — if it diffs, bump the pin to match the
  committed output), run `sqlc generate`, then
  `git diff --exit-code internal/store/sqlite/sqlc` → fails the job if the
  committed generated code is stale. Defends CLAUDE.md's "regenerate after editing
  queries/ or migrations/" rule.

### 4. `.github/workflows/release.yml`

Trigger: semver tags only — `v[0-9]+.[0-9]+.[0-9]+` and `v[0-9]+.[0-9]+.[0-9]+-*`
(prereleases). `permissions: {}` top-level. Single job `goreleaser`:

- Permissions: `contents: write` (release), `packages: write` (GHCR),
  `id-token: write` + `attestations: write` (keyless provenance).
- Steps: checkout `fetch-depth: 0` (changelog) + `persist-credentials: false`;
  setup-go (`go-version-file`); `docker/setup-qemu-action` + `docker/setup-buildx-action`
  (needed for the arm64 image's COPY-only build); `anchore/sbom-action/download-syft`;
  GHCR `docker/login-action` (`${{ github.actor }}` / `secrets.GITHUB_TOKEN`);
  `goreleaser/goreleaser-action` `version: ~> v2`, `args: release --clean`,
  `env GITHUB_TOKEN`.
- **Attestation**: `actions/attest-build-provenance` over `dist/checksums.txt`
  (binary archives). For the image: read the published manifest digest(s) from
  `dist/artifacts.json` (jq `select(.type=="Docker Manifest")` / published image
  entries) and attest with `subject-digest` + `push-to-registry: true`.
  *Fallback if digest extraction is brittle:* attest binaries only (checksums.txt)
  for the first release and revisit — acceptable, not a blocker.

### 5. `.goreleaser.yaml`

v2. Sections:

- **builds**: one build, `main: ./cmd/bfeed`, `binary: bfeed`,
  `env: [CGO_ENABLED=0]`, `flags: [-trimpath]`,
  `mod_timestamp: "{{ .CommitTimestamp }}"` (reproducible),
  `goos: [linux]`, `goarch: [amd64, arm64]`,
  `ldflags: ["-s -w -X main.version={{ .Version }}"]`.
- **archives**: `formats: [tar.gz]`, name template
  `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`,
  `files: [LICENSE, README.md]`.
- **checksum**: sha256, `checksums.txt`.
- **sboms**: `artifacts: archive` (Syft, per-archive).
- **dockers**: two entries (amd64, arm64), each `dockerfile: Dockerfile.release`,
  `use: buildx`, `goos: linux` + respective `goarch`, image template
  `ghcr.io/bcrisp4/bfeed:{{ .Version }}-{amd64|arm64}`, `build_flag_templates`
  carrying `--platform=linux/{arch}` and OCI labels
  (`org.opencontainers.image.{version,revision,source}`).
- **docker_manifests**: combine the two arch images into
  `ghcr.io/bcrisp4/bfeed:{{ .Version }}`, `:{{ .Major }}.{{ .Minor }}`, and
  `:latest` (prerelease-aware — `:latest` only for non-prerelease tags).
- **changelog**: `use: github`, exclude `^docs:`, `^test:`, `^chore:`, `^ci:`.
- **release**: `github: {owner: bcrisp4, name: bfeed}`, `prerelease: auto`.

### 6. `Dockerfile.release`

COPY-only distroless — goreleaser drops the prebuilt binary into the build
context, so **no build stage**:

```dockerfile
FROM gcr.io/distroless/static:nonroot
COPY bfeed /bfeed
ENV BFEED_DATABASE_PATH=/data/bfeed.db
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s CMD ["/bfeed", "healthcheck"]
ENTRYPOINT ["/bfeed"]
CMD ["serve"]
```

The existing `Dockerfile` (multi-stage, builds from source) stays for local
`docker build` dev. This works because the binary is fully self-contained
(migrations, templates, `static/` all `go:embed`-ed).

### 7. Repo edits

- **`go.mod`**: add `toolchain go1.26.4`.
- **`LICENSE`**: add full Apache-2.0 text. **`README.md`**: replace the
  "License: TBD" line with an Apache-2.0 link.
- **`CLAUDE.md`**: update the Commands section — the lint bar becomes
  `golangci-lint run` (gofumpt/goimports via golangci v2), `make` targets
  documented, sqlc-sync mentioned as CI-enforced.
- **One-time gofumpt reformat** of all existing `.go` files — **separate commit**,
  done before/independent of wiring CI, so the CI-enabling commits stay reviewable.

## Commit sequencing

1. `chore: gofumpt -extra reformat` — mechanical, isolated.
2. `build: add toolchain go1.26.4 + Apache-2.0 LICENSE` — `go.mod`, `LICENSE`, README.
3. `build: add Makefile and golangci config`.
4. `ci: add CI workflow (test, lint, sqlc-sync)`.
5. `ci: add release pipeline (goreleaser binaries + GHCR image)` — `.goreleaser.yaml`,
   `Dockerfile.release`, `release.yml`.
6. `docs: update CLAUDE.md build/lint commands`.

(Exact grouping can flex; the gofumpt reformat MUST stand alone.)

## Verification

- `go build ./...` (CGO off) and `go test ./... -race` green locally.
- `golangci-lint run` clean after reformat.
- `make sqlc-check` clean (generated code in sync).
- `goreleaser check` passes; `goreleaser release --snapshot --clean --skip=sbom`
  produces both binary archives and both arch images locally.
- CI green on a PR; release dry-run validated before cutting a real tag.

## Open risks

- **Image-digest attestation** from `dist/artifacts.json` may need iteration across
  goreleaser versions — fallback (binary-only attestation) documented above.
- **gofumpt reformat churn** could be sizeable; isolating it in commit 1 keeps the
  rest reviewable.
- **sqlc version pin** in CI must match the version used to generate the committed
  code, or the sync check produces spurious diffs. Pinned to v1.31.1 (locally
  installed); no version stamp in the generated header to confirm against, so the
  first CI run is the real test.
