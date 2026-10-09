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

CREATE TABLE event_history (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    source_event_id TEXT UNIQUE,
    event_type TEXT NOT NULL,
    severity TEXT NOT NULL,
    severity_rank INTEGER NOT NULL,
    category TEXT NOT NULL,
    created_at_ns INTEGER NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE INDEX idx_history_time ON event_history(created_at_ns DESC,id);
CREATE INDEX idx_history_type_time ON event_history(event_type,created_at_ns DESC,id);
CREATE INDEX idx_history_category_time ON event_history(category,created_at_ns DESC,id);
CREATE INDEX idx_history_severity_time ON event_history(severity_rank DESC,created_at_ns DESC,id);

CREATE TABLE alerts (
    id TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open','resolved')),
    updated_at_ns INTEGER NOT NULL,
    resolved_at_ns INTEGER,
    payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE UNIQUE INDEX idx_alert_open_fingerprint ON alerts(fingerprint) WHERE status='open';
CREATE INDEX idx_alert_status_updated ON alerts(status,updated_at_ns DESC,id);
CREATE TABLE alert_events (source_event_id TEXT PRIMARY KEY);

CREATE TABLE legacy_imports (id TEXT PRIMARY KEY, completed_at_ms INTEGER NOT NULL);
CREATE TABLE gitops_runtime (id INTEGER PRIMARY KEY CHECK (id=1), payload TEXT NOT NULL CHECK (json_valid(payload)));
CREATE TABLE nodes (id TEXT PRIMARY KEY, hostname TEXT NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload)));
CREATE INDEX idx_nodes_hostname ON nodes(hostname,id);
CREATE TABLE services (
    stack TEXT NOT NULL, name TEXT NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload)), PRIMARY KEY(stack,name)
);
CREATE TABLE secret_metadata (
    id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE TABLE recommendations (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
    stack TEXT NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE INDEX idx_recommendation_stack ON recommendations(stack,sequence);
CREATE TABLE assistant_chats (
    id TEXT PRIMARY KEY, updated_at_ns INTEGER NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE INDEX idx_chat_updated ON assistant_chats(updated_at_ns DESC,id);
CREATE TABLE assistant_turns (
    chat_id TEXT NOT NULL REFERENCES assistant_chats(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload)), PRIMARY KEY(chat_id,sequence)
);

CREATE TABLE deployments (
    id TEXT PRIMARY KEY,
    stack TEXT NOT NULL,
    revision TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','interrupted')),
    started_at_ns INTEGER NOT NULL,
    finished_at_ns INTEGER,
    effective_digest TEXT NOT NULL,
    desired_payload TEXT NOT NULL CHECK(json_valid(desired_payload)),
    payload TEXT NOT NULL CHECK(json_valid(payload)),
    CHECK((status='running' AND finished_at_ns IS NULL) OR (status<>'running' AND finished_at_ns IS NOT NULL))
);
CREATE INDEX idx_deployments_stack_time ON deployments(stack,started_at_ns DESC,id);
CREATE INDEX idx_deployments_time ON deployments(started_at_ns DESC,id);
CREATE UNIQUE INDEX idx_deployments_running_stack ON deployments(stack) WHERE status='running';

CREATE TABLE desired_snapshots (
    deployment_id TEXT PRIMARY KEY REFERENCES deployments(id),
    stack TEXT NOT NULL UNIQUE,
    effective_digest TEXT NOT NULL,
    payload TEXT NOT NULL CHECK(json_valid(payload))
);
-- External metadata inspection precedes this atomic projection/child-event receipt.
CREATE TABLE service_catalog_receipts (
    source_event_id TEXT PRIMARY KEY
);
-- Per-resource high-water marks prevent delayed retries from regressing projections.
CREATE TABLE projection_versions (
    projection TEXT NOT NULL,
    resource TEXT NOT NULL,
    occurred_at_ms INTEGER NOT NULL,
    source_event_id TEXT NOT NULL,
    PRIMARY KEY(projection,resource)
);
-- A known node reconnecting is never projected as a new node joining.
CREATE TABLE node_identities (id TEXT PRIMARY KEY);
