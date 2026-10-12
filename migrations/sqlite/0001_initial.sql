-- +goose Up
-- Initial SQLite schema.
-- Outbox is a queue: the last acknowledgement deletes the event immediately.
CREATE TABLE outbox_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
    occurred_at TIMESTAMP NOT NULL CHECK (typeof(occurred_at) = 'integer'),
    causation_id TEXT,
    payload TEXT NOT NULL CHECK (json_valid(payload))
);

CREATE TABLE outbox_deliveries (
    event_id TEXT NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
    subscription_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMP NOT NULL CHECK (typeof(available_at) = 'integer'),
    lease_until TIMESTAMP CHECK (lease_until IS NULL OR typeof(lease_until) = 'integer'),
    lease_token TEXT,
    last_error TEXT,
    updated_at TIMESTAMP NOT NULL CHECK (typeof(updated_at) = 'integer'),
    PRIMARY KEY (event_id, subscription_id),
    CHECK (
        (status = 'processing' AND lease_until IS NOT NULL AND lease_token IS NOT NULL)
        OR (status <> 'processing' AND lease_until IS NULL AND lease_token IS NULL)
    )
);
CREATE INDEX idx_outbox_delivery_due ON outbox_deliveries(status, available_at);
CREATE INDEX idx_outbox_delivery_expired_lease ON outbox_deliveries(status, lease_until);
CREATE INDEX idx_outbox_active_subscription
    ON outbox_deliveries(subscription_id,event_id)
    WHERE status IN ('pending','processing');

CREATE TABLE event_history (
    publication_sequence INTEGER PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    severity TEXT NOT NULL,
    severity_rank INTEGER NOT NULL,
    category TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL CHECK (typeof(created_at) = 'integer'),
    message TEXT NOT NULL,
    details TEXT CHECK (json_valid(details))
);
CREATE INDEX idx_history_time ON event_history(created_at DESC,id);
CREATE INDEX idx_history_type_time ON event_history(event_type,created_at DESC,id);
CREATE INDEX idx_history_category_time ON event_history(category,created_at DESC,id);
CREATE INDEX idx_history_severity_time ON event_history(severity_rank DESC,created_at DESC,id);

-- Indexed alert identity and lifecycle fields are stored only in columns.
-- payload contains title, message, failure details, occurrence count, event IDs and resolution.
CREATE TABLE alerts (
    id TEXT NOT NULL PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind <> ''),
    resource_type TEXT NOT NULL CHECK (resource_type <> ''),
    resource_id TEXT NOT NULL CHECK (resource_id <> ''),
    deployment_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('open','resolved')),
    opened_at TIMESTAMP NOT NULL CHECK (typeof(opened_at) = 'integer'),
    updated_at TIMESTAMP NOT NULL CHECK (typeof(updated_at) = 'integer'),
    resolved_at TIMESTAMP CHECK (resolved_at IS NULL OR typeof(resolved_at) = 'integer'),
    payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE UNIQUE INDEX idx_alert_open_fingerprint ON alerts(fingerprint) WHERE status='open';
CREATE INDEX idx_alert_status_updated ON alerts(status,updated_at DESC,id);
CREATE INDEX idx_alert_kind_status_updated ON alerts(kind,status,updated_at DESC,id);
CREATE INDEX idx_alert_resource_status_updated
    ON alerts(resource_type,resource_id,status,updated_at DESC,id);
CREATE INDEX idx_alert_deployment_updated
    ON alerts(deployment_id,updated_at DESC,id) WHERE deployment_id IS NOT NULL;

CREATE TABLE gitops_runtime (id INTEGER PRIMARY KEY CHECK (id=1), payload TEXT NOT NULL CHECK (json_valid(payload)));
-- id and hostname live only in columns; payload holds the remaining node attributes.
CREATE TABLE nodes (id TEXT NOT NULL PRIMARY KEY, hostname TEXT NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload)));
CREATE INDEX idx_nodes_hostname ON nodes(hostname,id);
-- stack and name live only in columns; payload holds metadata, image, environment, spec and web_routes.
CREATE TABLE services (
    stack TEXT NOT NULL, name TEXT NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload)), PRIMARY KEY(stack,name)
);
CREATE TABLE service_catalog_receipts (
    source_event_id TEXT NOT NULL PRIMARY KEY
);
-- id, name and timestamps live only in columns; payload holds version, driver and source Docker labels.
-- description, managed, external_path and external_version_id are derived from labels on read.
CREATE TABLE secret_metadata (
    id TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL CHECK (typeof(created_at) = 'integer'),
    updated_at TIMESTAMP NOT NULL CHECK (typeof(updated_at) = 'integer'),
    payload TEXT NOT NULL CHECK (json_valid(payload))
);
-- Fully relational; identity is (stack, service, source_file, type), sequence preserves order of first appearance.
CREATE TABLE recommendations (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    severity TEXT NOT NULL,
    type TEXT NOT NULL,
    stack TEXT NOT NULL,
    service TEXT NOT NULL,
    source_file TEXT NOT NULL,
    source_digest TEXT NOT NULL,
    source_commit TEXT NOT NULL,
    title TEXT NOT NULL,
    recommendation TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL CHECK (typeof(created_at) = 'integer'),
    UNIQUE (stack, service, source_file, type)
);
CREATE INDEX idx_recommendation_stack ON recommendations(stack,sequence);
CREATE TABLE assistant_chats (
    id TEXT NOT NULL PRIMARY KEY,
    title TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL CHECK (typeof(created_at) = 'integer'),
    updated_at TIMESTAMP NOT NULL CHECK (typeof(updated_at) = 'integer'),
    input_tokens INTEGER,
    output_tokens INTEGER,
    total_tokens INTEGER,
    CHECK (
        (input_tokens IS NULL AND output_tokens IS NULL AND total_tokens IS NULL)
        OR (input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND total_tokens IS NOT NULL)
    )
);
CREATE INDEX idx_chat_updated ON assistant_chats(updated_at DESC,id);
CREATE TABLE assistant_turns (
    chat_id TEXT NOT NULL REFERENCES assistant_chats(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL, payload TEXT NOT NULL CHECK (json_valid(payload)), PRIMARY KEY(chat_id,sequence)
);

CREATE TABLE deployments (
    id TEXT NOT NULL PRIMARY KEY,
    stack TEXT NOT NULL,
    "commit" TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','interrupted')),
    error_code TEXT NOT NULL,
    phase TEXT NOT NULL CHECK(phase IN ('apply','verification','cleanup','completed')),
    started_at TIMESTAMP NOT NULL CHECK (typeof(started_at) = 'integer'),
    finished_at TIMESTAMP CHECK (finished_at IS NULL OR typeof(finished_at) = 'integer'),
    desired_payload TEXT NOT NULL CHECK(json_valid(desired_payload)),
    details_payload TEXT NOT NULL CHECK(json_valid(details_payload)),
    changes_payload TEXT NOT NULL CHECK(json_valid(changes_payload) AND json_type(changes_payload) = 'array'),
    CHECK((status='running' AND finished_at IS NULL) OR (status<>'running' AND finished_at IS NOT NULL))
);
CREATE INDEX idx_deployments_stack_time ON deployments(stack,started_at DESC,id);
CREATE INDEX idx_deployments_time ON deployments(started_at DESC,id);
CREATE INDEX idx_deployments_status_time ON deployments(status,started_at DESC,id);
CREATE UNIQUE INDEX idx_deployments_running_stack ON deployments(stack) WHERE status='running';

CREATE TABLE desired_snapshots (
    stack TEXT NOT NULL PRIMARY KEY,
    deployment_id TEXT NOT NULL UNIQUE REFERENCES deployments(id)
);
-- Per-resource high-water marks prevent delayed retries from regressing projections.
CREATE TABLE projection_versions (
    projection TEXT NOT NULL,
    resource TEXT NOT NULL,
    occurred_at TIMESTAMP NOT NULL CHECK (typeof(occurred_at) = 'integer'),
    source_event_id TEXT NOT NULL,
    PRIMARY KEY(projection,resource)
);
-- A known node reconnecting is never projected as a new node joining.
CREATE TABLE node_identities (id TEXT NOT NULL PRIMARY KEY);

-- +goose Down
DROP TABLE node_identities;
DROP TABLE projection_versions;
DROP TABLE desired_snapshots;
DROP TABLE deployments;
DROP TABLE assistant_turns;
DROP TABLE assistant_chats;
DROP TABLE recommendations;
DROP TABLE secret_metadata;
DROP TABLE service_catalog_receipts;
DROP TABLE services;
DROP TABLE nodes;
DROP TABLE gitops_runtime;
DROP TABLE alerts;
DROP TABLE event_history;
DROP TABLE outbox_deliveries;
DROP TABLE outbox_events;
