-- +goose Up
CREATE TABLE deployment_attempts (
    id TEXT NOT NULL PRIMARY KEY,
    deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    number INTEGER NOT NULL CHECK(number > 0),
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','interrupted')),
    error_code TEXT NOT NULL,
    phase TEXT NOT NULL CHECK(phase IN ('apply','verification','cleanup','completed')),
    started_at TIMESTAMP NOT NULL CHECK (typeof(started_at) = 'integer'),
    finished_at TIMESTAMP CHECK (finished_at IS NULL OR typeof(finished_at) = 'integer'),
    details_payload TEXT NOT NULL CHECK(json_valid(details_payload)),
    CHECK((status='running' AND finished_at IS NULL) OR (status<>'running' AND finished_at IS NOT NULL)),
    UNIQUE(deployment_id, number)
);

CREATE INDEX idx_deployment_attempts_deployment ON deployment_attempts(deployment_id,number DESC);
CREATE INDEX idx_deployment_attempts_status_time ON deployment_attempts(status,started_at DESC,id);
CREATE UNIQUE INDEX idx_deployment_attempts_running_deployment ON deployment_attempts(deployment_id)
    WHERE status='running';

-- +goose Down
-- Forward-only by design: dropping the table would delete Attempt history.
SELECT * FROM deployment_attempts_migration_0002_is_forward_only;
