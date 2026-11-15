-- +goose Up
-- Services each Attempt executed. Queries filter deployment history by service
-- here instead of parsing Attempt and snapshot payloads. Rows with a NULL
-- attempt_id describe deployments recorded before Attempts were persisted.
CREATE TABLE deployment_services (
    deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    attempt_id TEXT REFERENCES deployment_attempts(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL CHECK(service_name <> '')
);

CREATE UNIQUE INDEX idx_deployment_services_attempt ON deployment_services(attempt_id, service_name)
    WHERE attempt_id IS NOT NULL;
CREATE UNIQUE INDEX idx_deployment_services_legacy ON deployment_services(deployment_id, service_name)
    WHERE attempt_id IS NULL;
CREATE INDEX idx_deployment_services_deployment ON deployment_services(deployment_id, service_name);
CREATE INDEX idx_deployment_services_service ON deployment_services(service_name, deployment_id);

-- Attempts with explicit targets executed only those services.
INSERT OR IGNORE INTO deployment_services (deployment_id, attempt_id, service_name)
SELECT attempt.deployment_id, attempt.id, target.value
FROM deployment_attempts AS attempt
    JOIN json_each(attempt.details_payload, '$.target_services') AS target
WHERE json_type(attempt.details_payload, '$.target_services') = 'array' AND target.value <> '';

-- Historical whole-stack Attempts executed every service of their snapshot.
INSERT OR IGNORE INTO deployment_services (deployment_id, attempt_id, service_name)
SELECT attempt.deployment_id, attempt.id, json_extract(svc.value, '$.name')
FROM deployment_attempts AS attempt
    JOIN deployments AS deployment ON deployment.id = attempt.deployment_id
    JOIN json_each(deployment.desired_payload, '$.definition.compose.services') AS svc
WHERE COALESCE(json_type(attempt.details_payload, '$.target_services'), 'null') = 'null'
    AND COALESCE(json_extract(svc.value, '$.name'), '') <> '';

-- Deployments recorded before Attempts were persisted were whole-stack executions.
INSERT OR IGNORE INTO deployment_services (deployment_id, attempt_id, service_name)
SELECT deployment.id, NULL, json_extract(svc.value, '$.name')
FROM deployments AS deployment
    JOIN json_each(deployment.desired_payload, '$.definition.compose.services') AS svc
WHERE NOT EXISTS (SELECT 1 FROM deployment_attempts AS attempt WHERE attempt.deployment_id = deployment.id)
    AND COALESCE(json_extract(svc.value, '$.name'), '') <> '';

-- +goose Down
-- The table is derived from Attempt and snapshot payloads and is rebuilt on Up.
DROP TABLE deployment_services;
