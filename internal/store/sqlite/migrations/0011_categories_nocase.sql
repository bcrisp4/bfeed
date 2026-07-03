-- +goose Up
-- Category uniqueness must match the case-insensitive collation the list query
-- and UI already use, so 'News' and 'news' can't coexist as visual duplicates
-- (audit B8). Additive unique index; it subsumes the original case-sensitive
-- UNIQUE(user_id, title) table constraint, which stays as harmless redundancy.
--
-- First fold away any case-variant duplicates an existing install already has
-- (the very state this fixes) so the index can build: re-point feeds off the
-- non-canonical rows to the lowest id in each (user_id, lower(title)) group,
-- then delete the losers. Order matters — re-point while all rows still exist,
-- then delete, so the feeds.category_id FK (ON DELETE SET NULL) never fires.
-- Plain UPDATE/DELETE/CREATE INDEX (no table rebuild), so the default goose
-- transaction is fine.
UPDATE feeds SET category_id = (
  SELECT MIN(c.id) FROM categories c
  JOIN categories self ON self.id = feeds.category_id
  WHERE c.user_id = self.user_id AND c.title = self.title COLLATE NOCASE
)
WHERE category_id IS NOT NULL;

DELETE FROM categories WHERE id NOT IN (
  SELECT MIN(id) FROM categories GROUP BY user_id, title COLLATE NOCASE
);

CREATE UNIQUE INDEX idx_categories_user_title_nocase
  ON categories(user_id, title COLLATE NOCASE);

-- +goose Down
DROP INDEX idx_categories_user_title_nocase;
