package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists service metadata by an unambiguous stack/name key.
type SQLStore struct{ db *storage.Database }

// NewSQLStore creates a service repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns the service catalog in stack/name order.
func (s *SQLStore) ReadAll(ctx context.Context) ([]model.Info, error) {
	rows, err := storage.QueryJSON[storeInfo](ctx, s.db.Get, "SELECT payload FROM services ORDER BY stack,name")
	if err != nil {
		return nil, err
	}
	result := make([]model.Info, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.toInfo())
	}
	return result, nil
}

// Get returns one catalog entry without hiding SQL errors.
func (s *SQLStore) Find(ctx context.Context, stack, name string) (model.Info, bool, error) {
	row, err := storage.GetJSON[storeInfo](ctx, s.db.Get,
		"SELECT payload FROM services WHERE stack=? AND name=?", stack, name)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Info{}, false, nil
	}
	if err != nil {
		return model.Info{}, false, err
	}
	return row.toInfo(), true, nil
}

// ReplaceStack atomically replaces one stack's service metadata.
func (s *SQLStore) ReplaceStack(ctx context.Context, stack string, services []model.Info) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.db.Get(ctx).ExecContext(ctx, "DELETE FROM services WHERE stack=?", stack); err != nil {
			return err
		}
		for _, row := range storeInfosFromServiceInfos(services) {
			if row.Name == "" {
				return fmt.Errorf("service name is required")
			}
			row.Stack = stack
			payload, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if _, err = s.db.Get(ctx).ExecContext(ctx,
				"INSERT INTO services VALUES (?,?,?)", stack, row.Name, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}

