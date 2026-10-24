-- +goose Up
-- Safe notification routing metadata survives configuration changes so terminal
-- delivery outcomes can still be correlated without exposing recipient details.
ALTER TABLE outbox_deliveries ADD COLUMN notification_destination TEXT;
ALTER TABLE outbox_deliveries ADD COLUMN notification_channel TEXT;
ALTER TABLE outbox_deliveries ADD COLUMN notification_destination_key TEXT;

-- Existing projection storage also holds sequence-based high-water marks for
-- notification delivery recovery. Timestamp-based projections keep using the
-- original columns.
ALTER TABLE projection_versions ADD COLUMN publication_sequence INTEGER NOT NULL DEFAULT 0
    CHECK (publication_sequence >= 0);

-- +goose Down
ALTER TABLE projection_versions DROP COLUMN publication_sequence;
ALTER TABLE outbox_deliveries DROP COLUMN notification_destination_key;
ALTER TABLE outbox_deliveries DROP COLUMN notification_channel;
ALTER TABLE outbox_deliveries DROP COLUMN notification_destination;
