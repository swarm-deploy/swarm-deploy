-- +goose Up
-- Consecutive observations must survive restarts even before an alert reaches
-- its opening threshold. Source-event idempotency remains in projection_versions.
CREATE TABLE alert_failure_streaks (
    fingerprint TEXT NOT NULL PRIMARY KEY,
    failures INTEGER NOT NULL CHECK (failures > 0)
);

-- +goose Down
DROP TABLE alert_failure_streaks;
