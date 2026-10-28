package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists service metadata by an unambiguous stack/name key.
type SQLStore struct{ db storage.Database }

// serviceRow is the raw columns of a services row for sqlx scanning.
type serviceRow struct {
	Stack   string `db:"stack"`
	Name    string `db:"name"`
	Payload []byte `db:"payload"`
}

// NewSQLStore creates a service repository.
func NewSQLStore(db storage.Database) *SQLStore { return &SQLStore{db: db} }

// List returns the service catalog in stack/name order.
func (s *SQLStore) ReadAll(ctx context.Context) ([]model.Info, error) {
	query, _, err := s.db.Dialect().From("services").Prepared(true).
		Select("stack", "name", "payload").
		Order(goqu.I("stack").Asc(), goqu.I("name").Asc()).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []serviceRow
	if err = s.db.Select(ctx, &rows, query); err != nil {
		return nil, err
	}
	result := make([]model.Info, 0, len(rows))
	for _, raw := range rows {
		var row storeInfo
		if err = json.Unmarshal(raw.Payload, &row); err != nil {
			return nil, err
		}
		result = append(result, row.toInfo(raw.Stack, raw.Name))
	}
	return result, nil
}

// Get returns one catalog entry without hiding SQL errors.
func (s *SQLStore) Find(ctx context.Context, stack, name string) (model.Info, bool, error) {
	query, args, err := s.db.Dialect().From("services").Prepared(true).
		Select("payload").Where(goqu.Ex{"stack": stack, "name": name}).ToSQL()
	if err != nil {
		return model.Info{}, false, err
	}
	var payload []byte
	err = s.db.Get(ctx, &payload, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Info{}, false, nil
	}
	if err != nil {
		return model.Info{}, false, err
	}
	var row storeInfo
	if err = json.Unmarshal(payload, &row); err != nil {
		return model.Info{}, false, err
	}
	return row.toInfo(stack, name), true, nil
}

// ReplaceStack atomically replaces one stack's service metadata.
func (s *SQLStore) ReplaceStack(ctx context.Context, stack string, services []model.Info) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		deleteQuery, deleteArgs, err := s.db.Dialect().Delete("services").Prepared(true).
			Where(goqu.Ex{"stack": stack}).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery, deleteArgs...); err != nil {
			return err
		}
		for _, info := range services {
			if info.Name == "" {
				return fmt.Errorf("service name is required")
			}
			payload, marshalErr := json.Marshal(storeInfoFromServiceInfo(info))
			if marshalErr != nil {
				return marshalErr
			}
			insertQuery, insertArgs, insertErr := s.db.Dialect().Insert("services").Prepared(true).
				Rows(goqu.Record{"stack": stack, "name": info.Name, "payload": string(payload)}).ToSQL()
			if insertErr != nil {
				return insertErr
			}
			if _, err = s.db.Exec(ctx, insertQuery, insertArgs...); err != nil {
				return err
			}
		}
		return nil
	})
}
