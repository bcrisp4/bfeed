-- +goose Up
CREATE TABLE categories (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  UNIQUE (user_id, title)
) STRICT;
CREATE INDEX idx_categories_user ON categories(user_id);

ALTER TABLE feeds ADD COLUMN category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL;
CREATE INDEX idx_feeds_category ON feeds(category_id);

-- supports the all-statuses category/uncategorised stream sort-free
CREATE INDEX idx_entries_user_pub ON entries(user_id, published_at DESC, id DESC);

-- +goose Down
DROP INDEX idx_entries_user_pub;
DROP INDEX idx_feeds_category;
ALTER TABLE feeds DROP COLUMN category_id;
DROP INDEX idx_categories_user;
DROP TABLE categories;
