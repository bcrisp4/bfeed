-- +goose Up
CREATE INDEX idx_entries_readhist ON entries(user_id, read_at DESC, id DESC) WHERE read_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_entries_readhist;
