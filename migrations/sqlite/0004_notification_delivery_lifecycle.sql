-- +goose Up
-- Safe notification routing metadata survives configuration changes so terminal
-- delivery outcomes can still be correlated without exposing recipient details.
ALTER TABLE outbox_deliveries ADD COLUMN notification_destination TEXT;
ALTER TABLE outbox_deliveries ADD COLUMN notification_channel TEXT;
ALTER TABLE outbox_deliveries ADD COLUMN notification_destination_key TEXT;

-- +goose Down
ALTER TABLE outbox_deliveries DROP COLUMN notification_destination_key;
ALTER TABLE outbox_deliveries DROP COLUMN notification_channel;
ALTER TABLE outbox_deliveries DROP COLUMN notification_destination;
