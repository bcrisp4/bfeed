-- +goose Up
ALTER TABLE feeds ADD COLUMN user_title TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE feeds DROP COLUMN user_title;
