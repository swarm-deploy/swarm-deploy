-- +goose Up
CREATE TABLE gitops_stack_sync_pauses (
    stack_name TEXT NOT NULL PRIMARY KEY,
    expires_at TIMESTAMP
);

-- +goose Down
DROP TABLE gitops_stack_sync_pauses;
