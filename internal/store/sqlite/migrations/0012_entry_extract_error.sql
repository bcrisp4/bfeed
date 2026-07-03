-- +goose Up
-- Persist the last extraction-failure reason so a terminally-'failed' entry is
-- diagnosable from the DB and can be surfaced in the reader (audit B10). Plain
-- ADD COLUMN with a default on a STRICT table — no table rebuild, so the default
-- goose transaction is fine (no NO TRANSACTION / PRAGMA dance needed).
ALTER TABLE entries ADD COLUMN extract_error TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE entries DROP COLUMN extract_error;
