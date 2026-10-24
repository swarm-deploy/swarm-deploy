-- +goose Up
-- Monitoring metadata is decided when a delivery is enqueued so restarts and
-- configuration changes cannot turn recursive alert notifications into
-- monitored deliveries.
ALTER TABLE outbox_deliveries ADD COLUMN monitor_delivery INTEGER NOT NULL DEFAULT 0
    CHECK (monitor_delivery IN (0,1));
ALTER TABLE outbox_deliveries ADD COLUMN destination TEXT;
ALTER TABLE outbox_deliveries ADD COLUMN channel TEXT;
-- Retained across replay so recovery is emitted only after an actual ack.
ALTER TABLE outbox_deliveries ADD COLUMN terminal_failed_at TIMESTAMP
    CHECK (terminal_failed_at IS NULL OR typeof(terminal_failed_at) = 'integer');

CREATE INDEX idx_outbox_terminal_failure_subscription
    ON outbox_deliveries(subscription_id,status)
    WHERE monitor_delivery=1 AND terminal_failed_at IS NOT NULL AND status<>'delivered';

-- +goose Down
DROP INDEX idx_outbox_terminal_failure_subscription;
ALTER TABLE outbox_deliveries DROP COLUMN terminal_failed_at;
ALTER TABLE outbox_deliveries DROP COLUMN channel;
ALTER TABLE outbox_deliveries DROP COLUMN destination;
ALTER TABLE outbox_deliveries DROP COLUMN monitor_delivery;
