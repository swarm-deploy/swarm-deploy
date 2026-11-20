-- +goose Up
ALTER TABLE assistant_chats ADD COLUMN created_by TEXT;

-- +goose Down
ALTER TABLE assistant_chats DROP COLUMN created_by;
