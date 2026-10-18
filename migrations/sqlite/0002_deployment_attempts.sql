-- +goose Up
ALTER TABLE desired_snapshots RENAME TO legacy_desired_snapshots;
ALTER TABLE deployments RENAME TO legacy_deployments;

CREATE TABLE deployments (
    id TEXT NOT NULL PRIMARY KEY,
    stack TEXT NOT NULL,
    "commit" TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','interrupted','superseded')),
    error_code TEXT NOT NULL,
    phase TEXT NOT NULL CHECK(phase IN ('apply','verification','cleanup','completed')),
    started_at TIMESTAMP NOT NULL CHECK (typeof(started_at) = 'integer'),
    finished_at TIMESTAMP CHECK (finished_at IS NULL OR typeof(finished_at) = 'integer'),
    desired_payload TEXT NOT NULL CHECK(json_valid(desired_payload)),
    details_payload TEXT NOT NULL CHECK(json_valid(details_payload)),
    changes_payload TEXT NOT NULL CHECK(json_valid(changes_payload) AND json_type(changes_payload) = 'array'),
    latest_attempt_id TEXT NOT NULL,
    attempt_count INTEGER NOT NULL CHECK(attempt_count > 0),
    CHECK((status='running' AND finished_at IS NULL) OR (status<>'running' AND finished_at IS NOT NULL))
);

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

INSERT INTO deployments (
    id, stack, "commit", status, error_code, phase, started_at, finished_at,
    desired_payload, details_payload, changes_payload, latest_attempt_id, attempt_count
)
SELECT id, stack, "commit", status, error_code, phase, started_at, finished_at,
       desired_payload, details_payload, changes_payload, id || '-attempt-1', 1
FROM legacy_deployments
ORDER BY rowid;

INSERT INTO deployment_attempts (
    id, deployment_id, number, status, error_code, phase, started_at, finished_at, details_payload
)
SELECT id || '-attempt-1', id, 1, status, error_code, phase, started_at, finished_at, details_payload
FROM legacy_deployments;

-- Legacy retries were separate deployments. Preserve every row and its attempt,
-- but close older unfinished projections when a newer deployment exists for the
-- same stack so the new single-active-deployment invariant can be established.
UPDATE deployments AS deployment
SET status = 'superseded'
WHERE status IN ('failed', 'interrupted')
  AND EXISTS (
      SELECT 1
      FROM deployments AS newer
      WHERE newer.stack = deployment.stack
        AND newer.rowid > deployment.rowid
  );

CREATE TABLE desired_snapshots (
    stack TEXT NOT NULL PRIMARY KEY,
    deployment_id TEXT NOT NULL UNIQUE REFERENCES deployments(id)
);
INSERT INTO desired_snapshots(stack, deployment_id)
SELECT stack, deployment_id FROM legacy_desired_snapshots;

DROP TABLE legacy_desired_snapshots;
DROP TABLE legacy_deployments;

CREATE INDEX idx_deployments_stack_time ON deployments(stack,started_at DESC,id);
CREATE INDEX idx_deployments_time ON deployments(started_at DESC,id);
CREATE INDEX idx_deployments_status_time ON deployments(status,started_at DESC,id);
CREATE UNIQUE INDEX idx_deployments_active_stack ON deployments(stack)
    WHERE status IN ('running','failed','interrupted');
CREATE INDEX idx_deployment_attempts_deployment ON deployment_attempts(deployment_id,number DESC);
CREATE INDEX idx_deployment_attempts_status_time ON deployment_attempts(status,started_at DESC,id);
CREATE UNIQUE INDEX idx_deployment_attempts_running_deployment ON deployment_attempts(deployment_id)
    WHERE status='running';

-- +goose Down
ALTER TABLE desired_snapshots RENAME TO current_desired_snapshots;
ALTER TABLE deployments RENAME TO current_deployments;

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

INSERT INTO deployments (
    id, stack, "commit", status, error_code, phase, started_at, finished_at,
    desired_payload, details_payload, changes_payload
)
SELECT id, stack, "commit",
       CASE WHEN status='superseded' THEN 'interrupted' ELSE status END,
       CASE WHEN status='superseded' THEN 'superseded' ELSE error_code END,
       phase, started_at,
       CASE WHEN finished_at IS NULL THEN started_at ELSE finished_at END,
       desired_payload, details_payload, changes_payload
FROM current_deployments;

CREATE TABLE desired_snapshots (
    stack TEXT NOT NULL PRIMARY KEY,
    deployment_id TEXT NOT NULL UNIQUE REFERENCES deployments(id)
);
INSERT INTO desired_snapshots(stack, deployment_id)
SELECT stack, deployment_id FROM current_desired_snapshots;

DROP TABLE deployment_attempts;
DROP TABLE current_desired_snapshots;
DROP TABLE current_deployments;

CREATE INDEX idx_deployments_stack_time ON deployments(stack,started_at DESC,id);
CREATE INDEX idx_deployments_time ON deployments(started_at DESC,id);
CREATE INDEX idx_deployments_status_time ON deployments(status,started_at DESC,id);
CREATE UNIQUE INDEX idx_deployments_running_stack ON deployments(stack) WHERE status='running';
