-- +goose Up
ALTER TABLE assistant_chats ADD COLUMN primary_artifact TEXT;

-- +goose Down
ALTER TABLE assistant_chats DROP COLUMN primary_artifact;
