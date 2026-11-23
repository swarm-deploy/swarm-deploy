-- +goose Up
ALTER TABLE outbox_events ADD COLUMN traceparent TEXT;

-- +goose Down
ALTER TABLE outbox_events DROP COLUMN traceparent;
