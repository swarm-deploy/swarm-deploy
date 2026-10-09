-- Single initial migration, extended in this integration branch until release.
-- Outbox is a queue: the last acknowledgement deletes the event immediately.
CREATE TABLE outbox_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
    occurred_at_ms INTEGER NOT NULL,
    correlation_id TEXT,
    causation_id TEXT,
    payload TEXT NOT NULL CHECK (json_valid(payload))
);

CREATE TABLE outbox_deliveries (
    event_id TEXT NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
    subscription_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at_ms INTEGER NOT NULL,
    lease_until_ms INTEGER,
    lease_token TEXT,
    last_error TEXT,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (event_id, subscription_id),
    CHECK (
        (status = 'processing' AND lease_until_ms IS NOT NULL AND lease_token IS NOT NULL)
        OR (status <> 'processing' AND lease_until_ms IS NULL AND lease_token IS NULL)
    )
);
CREATE INDEX idx_outbox_delivery_due ON outbox_deliveries(status, available_at_ms);
CREATE INDEX idx_outbox_delivery_expired_lease ON outbox_deliveries(status, lease_until_ms);
