-- +goose Up
CREATE TABLE deployment_init_jobs (
    attempt_id TEXT NOT NULL REFERENCES deployment_attempts(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK(ordinal > 0),
    service_name TEXT NOT NULL,
    job_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('succeeded','failed','interrupted')),
    started_at TIMESTAMP NOT NULL CHECK (typeof(started_at) = 'integer'),
    finished_at TIMESTAMP NOT NULL CHECK (typeof(finished_at) = 'integer'),
    logs BLOB,
    logs_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (attempt_id, ordinal)
);

-- +goose Down
DROP TABLE deployment_init_jobs;
