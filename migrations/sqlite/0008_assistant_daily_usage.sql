-- +goose Up
CREATE TABLE assistant_daily_usage (
    day TEXT NOT NULL PRIMARY KEY,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    total_tokens INTEGER NOT NULL
);

-- +goose Down
DROP TABLE assistant_daily_usage;
