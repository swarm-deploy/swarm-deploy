-- +goose Up
ALTER TABLE assistant_chats ADD COLUMN unread BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE assistant_chats DROP COLUMN unread;
