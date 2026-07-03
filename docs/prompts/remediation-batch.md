# Session kickoff prompt — audit remediation batch

Reusable prompt for starting a fresh session to work one batch of the 2026-07 codebase
audit (see [`docs/audit-2026-07.md`](../audit-2026-07.md)). Findings are tracked in GitHub
milestone **"Fable audit remediation"** (#1) as 13 batch issues, #26–#38.

## How to use

Copy the block below, replacing **`B2`** with the batch id and **`27`** with its issue
number. Batch → issue map (also in the report's summary table):

| Batch | B2 | B3 | B4 | B5 | B6 | B7 | B8 | B9 | B10 | B11 | B12 | B13 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Issue | #27 | #28 | #29 | #30 | #31 | #32 | #33 | #34 | #35 | #36 | #37 | #38 |

Recommended order (waves): **B2 → B3**, then **B4 → B5 → B6**, then brainstorm
**B7 → B8**, then **B9 → B10 → B11**, then **B12 → B13**. B1 (#26) is already merged.
Do **B2 before the other bug batches** — several later batches add tests against the
`coretest` fakes that B2 tightens.

## The prompt

```
Work audit-remediation batch B2 (GitHub issue #27) on the bfeed repo.

The batch's findings, with full repro and a suggested fix for each, are in
docs/audit-2026-07.md under the "Batch B2" section. Every finding there was
majority-confirmed by a three-skeptic adversarial verification panel, but code
moves — treat the report as a lead, not gospel.

Do this:
1. Read CLAUDE.md in full first, then the "Batch B2" section of docs/audit-2026-07.md
   and the issue (`gh issue view 27`). Honor every documented invariant and the
   ports-and-adapters rules; do not "fix" deliberate design. Cross-check against
   Appendix A (documented deferrals) and Appendix B (rejected findings) so you don't
   re-raise something already settled.
2. For each finding: open the cited file and CONFIRM the issue is still present in the
   current code (line numbers and severities drift; an earlier batch may have fixed it
   already). If it's gone or was a false positive, note that and skip it — don't force
   a change.
3. Fix each confirmed finding TDD: write a failing test that reproduces the defect
   (red), make it pass (green), refactor. stdlib `testing` only, no testify; reuse the
   `internal/core/coretest` fakes rather than adding new ones. Anything touching
   queries/ or migrations/ needs `make sqlc` plus a committed regen.
4. Group the whole batch into ONE feature branch and ONE PR. Add a CHANGELOG.md entry
   under [Unreleased], written from the user's perspective, in the right Keep-a-Changelog
   category — or apply the `skip-changelog` label if nothing is user-facing (pure
   refactors, tests, docs, tooling). Never name specific external feed/blog URLs in the
   changelog or commits; describe the condition generically.
5. Verify before claiming done: `make test-race`, `make lint`, and `make sqlc-check` all
   green. For anything with runtime behavior, exercise it end-to-end (drive the real
   flow), not just via tests.
6. Open the PR with `Closes #27` in the body, base main; wait for CI to pass. Do NOT
   self-merge — leave it for review. Report what you changed and, explicitly, any
   finding you skipped and why.

If this batch is marked [design call] (B7 rowid reuse / poll-edit races, B8 feed
identity & dedup): do NOT jump to code. Brainstorm the approach first (e.g.
AUTOINCREMENT vs a generation-guard column for rowid reuse; how aggressive to make
URL / GUID / category normalization), get a decision, then implement.

Batches are largely independent; if you hit a real dependency on an unmerged batch,
flag it rather than working around it.
```
