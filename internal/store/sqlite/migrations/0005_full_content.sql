-- +goose Up
ALTER TABLE feeds ADD COLUMN fetch_full_content INTEGER NOT NULL DEFAULT 0
  CHECK (fetch_full_content IN (0,1));

ALTER TABLE entries ADD COLUMN extract_state TEXT NOT NULL DEFAULT 'none'
  CHECK (extract_state IN ('none','pending','done','failed'));
ALTER TABLE entries ADD COLUMN extract_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE entries ADD COLUMN next_extract_at INTEGER;

-- pending-only partial index, pre-sorted (published_at DESC, id DESC): the scrape
-- sweep scans it in order (no temp B-tree) and stays tiny as entries leave 'pending'.
-- The trailing id DESC tiebreak keeps equal-published_at rows deterministically
-- ordered, matching every other entries index.
CREATE INDEX idx_entries_pending ON entries(published_at DESC, id DESC) WHERE extract_state = 'pending';

-- +goose Down
DROP INDEX idx_entries_pending;
ALTER TABLE entries DROP COLUMN next_extract_at;
ALTER TABLE entries DROP COLUMN extract_attempts;
ALTER TABLE entries DROP COLUMN extract_state;
ALTER TABLE feeds DROP COLUMN fetch_full_content;
