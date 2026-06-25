-- +goose Up
ALTER TABLE feeds ADD COLUMN ttl_seconds INTEGER;

-- +goose Down
ALTER TABLE feeds DROP COLUMN ttl_seconds;
