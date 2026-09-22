# Decisions

## 2026-09-23: Keep feed URL queries in OPML exports

**Context:** Some feed URLs use query values as access tokens.

**Decision:** Export complete query strings. Omit URL userinfo. Treat OPML files as private data.

**Rejected:** Remove selected credential parameters or all query strings.

**Why:** bfeed cannot identify every credential parameter. Removal can break imported subscriptions.

**Revisit when:** bfeed stores feed credentials separately from feed URLs.

**Links:** `docs/design.md` section 19 and `internal/core/opml.go`.
