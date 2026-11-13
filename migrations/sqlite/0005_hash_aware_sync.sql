-- +goose Up
-- Hash-aware synchronization deliberately starts with unknown applied state.
-- Historical successful baselines are insufficient evidence because older
-- executions could complete after applying only part of a stack.
CREATE TABLE deployment_stack_states (
    stack TEXT NOT NULL PRIMARY KEY,
    desired_digest TEXT NOT NULL CHECK (desired_digest <> ''),
    desired_version INTEGER NOT NULL CHECK (desired_version > 0),
    desired_commit TEXT NOT NULL,
    applied_digest TEXT,
    applied_version INTEGER,
    applied_commit TEXT,
    desired_resources_digest TEXT NOT NULL CHECK (desired_resources_digest <> ''),
    desired_resources_version INTEGER NOT NULL CHECK (desired_resources_version > 0),
    applied_resources_digest TEXT,
    applied_resources_version INTEGER,
    applied_resources_commit TEXT,
    resources_uncertain INTEGER NOT NULL DEFAULT 0 CHECK (resources_uncertain IN (0, 1)),
    CHECK ((applied_digest IS NULL) = (applied_version IS NULL)),
    CHECK ((applied_digest IS NULL) = (applied_commit IS NULL)),
    CHECK ((applied_resources_digest IS NULL) = (applied_resources_version IS NULL)),
    CHECK ((applied_resources_digest IS NULL) = (applied_resources_commit IS NULL))
);

CREATE TABLE deployment_service_states (
    stack TEXT NOT NULL REFERENCES deployment_stack_states(stack) ON DELETE CASCADE,
    service TEXT NOT NULL,
    desired_digest TEXT,
    desired_version INTEGER,
    desired_commit TEXT NOT NULL,
    applied_digest TEXT,
    applied_version INTEGER,
    applied_commit TEXT,
    applied_uncertain INTEGER NOT NULL DEFAULT 0 CHECK (applied_uncertain IN (0, 1)),
    PRIMARY KEY (stack, service),
    CHECK ((desired_digest IS NULL) = (desired_version IS NULL)),
    CHECK ((applied_digest IS NULL) = (applied_version IS NULL)),
    CHECK ((applied_digest IS NULL) = (applied_commit IS NULL)),
    CHECK (desired_digest IS NOT NULL OR applied_digest IS NOT NULL OR applied_uncertain = 1)
);
CREATE INDEX idx_deployment_service_states_pending
    ON deployment_service_states(stack, service)
    WHERE desired_digest IS NULL OR applied_digest IS NULL
       OR desired_digest <> applied_digest OR desired_version <> applied_version;

-- +goose Down
-- Forward-only by design: dropping these tables would discard applied-state evidence.
SELECT * FROM deployment_hash_state_migration_0005_is_forward_only;
