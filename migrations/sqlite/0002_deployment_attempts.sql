-- +goose Up
-- This migration is intentionally additive so an older swarm-deploy instance
-- can keep using the original deployments and desired_snapshots tables during
-- a start-first rolling update.
ALTER TABLE deployments ADD COLUMN latest_attempt_id TEXT;
ALTER TABLE deployments ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deployments ADD COLUMN superseded_at TIMESTAMP;

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

-- Existing rows remain legacy-owned while an old process may still be running:
-- NULL/0 in the additive projection columns is the compatibility marker. Reads
-- derive attempt 1 from the deterministic ID until a new binary starts a retry.
INSERT INTO deployment_attempts (
    id, deployment_id, number, status, error_code, phase, started_at, finished_at, details_payload
)
SELECT id || '-attempt-1', id, 1, status, error_code, phase, started_at, finished_at, details_payload
FROM deployments;

-- Preserve the original status and diagnostics. Supersession is a separate
-- projection and only older unfinished history is marked during the backfill.
UPDATE deployments AS deployment
SET superseded_at = finished_at
WHERE status IN ('failed', 'interrupted')
  AND superseded_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM deployments AS newer
      WHERE newer.stack = deployment.stack
        AND newer.rowid > deployment.rowid
  );

-- An old binary does not know about Attempts. Mirror its explicit-column INSERT
-- and later lifecycle UPDATE statements while leaving the compatibility marker
-- intact. New binaries set latest_attempt_id/attempt_count and skip these triggers.
-- +goose StatementBegin
CREATE TRIGGER deployment_attempts_legacy_insert
AFTER INSERT ON deployments
WHEN NEW.latest_attempt_id IS NULL AND NEW.attempt_count = 0
BEGIN
    UPDATE deployments
    SET superseded_at = NEW.started_at
    WHERE stack = NEW.stack
      AND id <> NEW.id
      AND superseded_at IS NULL
      AND status IN ('failed', 'interrupted');

    INSERT OR IGNORE INTO deployment_attempts (
        id, deployment_id, number, status, error_code, phase,
        started_at, finished_at, details_payload
    ) VALUES (
        NEW.id || '-attempt-1', NEW.id, 1, NEW.status, NEW.error_code, NEW.phase,
        NEW.started_at, NEW.finished_at, NEW.details_payload
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER deployment_attempts_legacy_update
AFTER UPDATE OF status, error_code, phase, started_at, finished_at, details_payload ON deployments
WHEN NEW.latest_attempt_id IS NULL AND NEW.attempt_count = 0
BEGIN
    INSERT OR IGNORE INTO deployment_attempts (
        id, deployment_id, number, status, error_code, phase,
        started_at, finished_at, details_payload
    ) VALUES (
        NEW.id || '-attempt-1', NEW.id, 1, NEW.status, NEW.error_code, NEW.phase,
        NEW.started_at, NEW.finished_at, NEW.details_payload
    );

    UPDATE deployment_attempts
    SET status = NEW.status,
        error_code = NEW.error_code,
        phase = NEW.phase,
        started_at = NEW.started_at,
        finished_at = NEW.finished_at,
        details_payload = NEW.details_payload
    WHERE id = NEW.id || '-attempt-1'
      AND deployment_id = NEW.id
      AND number = 1;
END;
-- +goose StatementEnd

CREATE INDEX idx_deployment_attempts_deployment ON deployment_attempts(deployment_id,number DESC);
CREATE INDEX idx_deployment_attempts_status_time ON deployment_attempts(status,started_at DESC,id);
CREATE UNIQUE INDEX idx_deployment_attempts_running_deployment ON deployment_attempts(deployment_id)
    WHERE status='running';

-- +goose Down
-- Forward-only by design: removing Attempts or additive columns would destroy
-- history and make a rolling application rollback unsafe. This deliberate error
-- keeps the schema version at 2 and documents that rollback is application-only.
SELECT * FROM deployment_attempts_migration_0002_is_forward_only;
