-- +goose Up
-- Per-entry discussion/comments URL captured from RSS <comments> (issue #92).
-- Poll-owned: UpsertEntries refreshes it when the entry's content hash changes,
-- exactly like url; deliberately NOT part of the entry hash, so a changed
-- comments link alone never rewrites an entry. Plain ADD COLUMN with a default
-- on a STRICT table needs no rebuild - the default goose transaction is fine
-- (same shape as 0012_entry_extract_error.sql).
ALTER TABLE entries ADD COLUMN comments_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE entries DROP COLUMN comments_url;
