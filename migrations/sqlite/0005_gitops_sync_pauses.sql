-- +goose Up
CREATE TABLE gitops_stack_sync_pauses (
    stack_name TEXT NOT NULL PRIMARY KEY,
    expires_at TIMESTAMP
);

CREATE TABLE gitops_service_sync_pauses (
    stack_name   TEXT NOT NULL,
    service_name TEXT NOT NULL,
    expires_at   TIMESTAMP,
    PRIMARY KEY (stack_name, service_name)
);

-- +goose Down
DROP TABLE gitops_service_sync_pauses;
DROP TABLE gitops_stack_sync_pauses;
