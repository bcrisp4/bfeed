# Changelog policy

bfeed keeps a human-curated changelog at [`CHANGELOG.md`](../CHANGELOG.md) in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. It is the single
source of truth for release notes — at release time the `[Unreleased]` section
becomes the GitHub Release body (no commit-message changelog is generated).

This document is the requirement for **every contributor, human or AI agent**.

## The rule

**Every pull request that changes behaviour adds an entry under `[Unreleased]`
in `CHANGELOG.md`.** CI enforces this (see below). Write the entry from the
*user's* point of view — what changed for someone running bfeed — not a restatement
of the commit. One net entry per change is better than one per commit.

Use the Keep a Changelog categories, in this order:

| Category     | Use for                                                        |
|--------------|----------------------------------------------------------------|
| `Added`      | new features                                                   |
| `Changed`    | changes to existing behaviour                                  |
| `Deprecated` | soon-to-be-removed features                                    |
| `Removed`    | removed features                                               |
| `Fixed`      | bug fixes                                                      |
| `Security`   | vulnerabilities fixed                                          |

Only include the categories you actually need. Keep entries terse and in the
present tense.

## When you may skip an entry

Some PRs genuinely have no user-facing change: CI/tooling tweaks, internal
refactors with no behaviour change, test-only changes, dependency bumps,
documentation. For those, apply the **`skip-changelog`** label to the PR and the
enforcing job is skipped. Prefer adding an entry when in doubt — a skipped label
is a deliberate "this changes nothing a user would notice".

## How CI enforces it

The `changelog` job in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)
runs on pull requests. It fails unless `CHANGELOG.md` appears in the PR's diff
against the base branch:

```bash
git diff --name-only "origin/${BASE_REF}...HEAD" | grep -qxF CHANGELOG.md
```

The job is skipped when the PR carries the `skip-changelog` label. It does not
run on direct pushes to `main` (a skipped required check still counts as passing
for branch protection).

Repo settings that back this up (one-time, outside the codebase):

- a `skip-changelog` label exists, and
- the `changelog` check is marked **required** on the protected `main` branch.

## How it flows into a release

The release procedure is in [`releasing.md`](releasing.md). In short, before
tagging you rename `[Unreleased]` to the new version and open a fresh empty
`[Unreleased]`:

```markdown
## [Unreleased]

## [0.2.0] - 2026-06-20

### Added
- ...
```

On the tag push, `release.yml` extracts that section's body into a notes file
and passes it to GoReleaser via `--release-notes`, so the GitHub Release body is
the section's entries (the `## [0.2.0]` heading itself is dropped — the release
is already titled with the version). `changelog.disable: true` in
[`.goreleaser.yaml`](../.goreleaser.yaml) turns off GoReleaser's own
commit-based changelog. If the section for the tag is missing, the release job
fails fast rather than publishing empty notes.

Also update the two compare/release links at the bottom of `CHANGELOG.md` when
you cut a version.
