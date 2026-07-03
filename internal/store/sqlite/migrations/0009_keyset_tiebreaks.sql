-- +goose Up
DROP INDEX idx_entries_feed_pub;
DROP INDEX idx_entries_starred;
CREATE INDEX idx_entries_feed_pub ON entries(feed_id, published_at DESC, id DESC);
CREATE INDEX idx_entries_starred  ON entries(user_id, published_at DESC, id DESC) WHERE starred = 1;

-- +goose Down
DROP INDEX idx_entries_feed_pub;
DROP INDEX idx_entries_starred;
CREATE INDEX idx_entries_feed_pub ON entries(feed_id, published_at DESC);
CREATE INDEX idx_entries_starred  ON entries(user_id, published_at DESC) WHERE starred = 1;
