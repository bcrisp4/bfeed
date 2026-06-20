# Releasing bfeed

Releases are cut by pushing an **annotated, semver git tag**. The `release`
GitHub Actions workflow (`.github/workflows/release.yml`) does the rest via
GoReleaser.

## Versioning

Tags are [semver](https://semver.org): `vMAJOR.MINOR.PATCH` (e.g. `v1.4.2`).
Prereleases append a suffix: `v1.4.2-rc1`, `v1.4.2-beta1`. The workflow only
fires on tags matching `v[0-9]+.[0-9]+.[0-9]+` (and `-*` prereleases) — stray
tags like `v1`, `vfoo`, or `v1.2.3.4` are ignored. Semver **build-metadata**
tags (`v1.2.3+build`) are deliberately *not* matched: OCI/Docker image tags
can't contain `+`, and build metadata is precedence-irrelevant.

## Cutting a release

Pre-flight: be on `main`, tree clean, CI green for the commit you're tagging.

First, roll the changelog. `CHANGELOG.md` is the single source of truth for
release notes (see [changelog.md](changelog.md)) — rename `[Unreleased]` to the
new version, open a fresh empty `[Unreleased]`, and update the compare/release
links at the bottom of the file:

```markdown
## [Unreleased]

## [1.4.2] - 2026-06-20

### Added
- ...
```

```text
[Unreleased]: https://github.com/bcrisp4/bfeed/compare/v1.4.2...HEAD
[1.4.2]: https://github.com/bcrisp4/bfeed/compare/v1.4.1...v1.4.2
```

Commit that to `main` (its own PR, or part of the release-prep PR), then tag:

```bash
git switch main && git pull --ff-only
make test-race && make lint && make sqlc-check   # local sanity
git tag -a v1.4.2 -m "v1.4.2"                     # annotated tag (NOT lightweight)
git push origin v1.4.2
```

On the tag push, `release.yml` extracts the `## [1.4.2]` section from
`CHANGELOG.md` into a notes file and hands it to GoReleaser via
`--release-notes`; that section's entries become the GitHub Release body (the
`## [1.4.2]` heading itself is dropped — the release is already titled with the
version). If no matching section exists, the release job fails fast rather than
publishing empty notes. Prereleases (`v1.4.2-rc1`) need a matching `## [1.4.2-rc1]` section, or
edit the release notes by hand afterwards.

Use an **annotated** tag (`-a`): GoReleaser and `git describe` rely on annotated
tags, and the tag message is a useful release anchor. Lightweight tags
(`git tag v1.4.2` with no `-a`) are discouraged.

Optionally dry-run the whole pipeline locally first (builds binaries + both-arch
images, no push). Requires a running Docker engine and a buildx builder that can
do multi-platform — Docker Desktop has one; on a plain Linux docker install run
`docker buildx create --use` first (CI provisions one via `setup-buildx-action`):

```bash
goreleaser release --snapshot --clean
```

## What the pipeline produces

On a matching tag push, `release.yml` runs GoReleaser, which:

- builds `linux/amd64` + `linux/arm64` binaries (`CGO_ENABLED=0`, `-trimpath`,
  version stamped via `-X main.version`);
- packages `tar.gz` archives (with `LICENSE` + `README.md`), `checksums.txt`,
  and a per-archive SBOM (Syft);
- builds and pushes a multi-arch image to GHCR with Docker buildx
  (goreleaser `dockers_v2`): `ghcr.io/bcrisp4/bfeed:<version>`, plus floating
  `:<major>.<minor>` and `:latest` (the floating tags are **skipped for
  prereleases**);
- creates the GitHub Release (prereleases auto-flagged) with the body taken
  from the tag's `CHANGELOG.md` section via `--release-notes`; and
- attests build provenance for the binaries (firm, over `checksums.txt`) and
  for the image manifest (best-effort).

## Verify a release

```bash
gh release view v1.4.2
docker pull ghcr.io/bcrisp4/bfeed:1.4.2
gh attestation verify oci://ghcr.io/bcrisp4/bfeed:1.4.2 --owner bcrisp4
```

## Building the image locally (dev)

For a quick local image off the multi-stage dev `Dockerfile` (host arch only,
no registry), use the Makefile target — it shells out to `docker`:

```bash
make image          # docker build -t bfeed:<version> .
```

## Fixing a botched release

Prefer rolling forward with a new patch tag (`v1.4.3`). If you must redo a tag
before anything depends on it:

```bash
git push origin :refs/tags/v1.4.2   # delete remote tag
git tag -d v1.4.2                    # delete local tag
gh release delete v1.4.2 --yes       # delete the GitHub release
# fix, then re-tag and re-push
```

Note: images already pushed to GHCR for that tag should be treated as
published — bump the patch version rather than mutating a release others may
have pulled.
