-- +goose Up
ALTER TABLE assistant_chats ADD COLUMN read_only BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE assistant_chats DROP COLUMN read_only;
