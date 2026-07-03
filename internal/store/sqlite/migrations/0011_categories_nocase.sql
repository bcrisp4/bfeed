-- +goose Up
-- Category uniqueness must match the case-insensitive collation the list query
-- and UI already use, so 'News' and 'news' can't coexist as visual duplicates
-- (audit B8). Additive unique index; it subsumes the original case-sensitive
-- UNIQUE(user_id, title) table constraint, which stays as harmless redundancy.
CREATE UNIQUE INDEX idx_categories_user_title_nocase
  ON categories(user_id, title COLLATE NOCASE);

-- +goose Down
DROP INDEX idx_categories_user_title_nocase;
