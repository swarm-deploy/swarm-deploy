package storage

import (
	"context"
	"encoding/json"

	"github.com/Thiht/transactor/stdlib"
)

// QueryJSON decodes a single JSON column from a module-owned SQL query.
func QueryJSON[T any](ctx context.Context, get stdlib.DBGetter, query string, args ...any) ([]T, error) {
	rows, err := get(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []T{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		var value T
		if err = json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

// GetJSON decodes a single row and preserves sql.ErrNoRows for the repository.
func GetJSON[T any](ctx context.Context, get stdlib.DBGetter, query string, args ...any) (T, error) {
	var value T
	var payload []byte
	if err := get(ctx).QueryRowContext(ctx, query, args...).Scan(&payload); err != nil {
		return value, err
	}
	err := json.Unmarshal(payload, &value)
	return value, err
}
