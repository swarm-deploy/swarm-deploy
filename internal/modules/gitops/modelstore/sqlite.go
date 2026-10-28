package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists runtime state synchronously in the caller's transaction.
type SQLStore struct{ db storage.Database }

// NewSQLStore creates a runtime repository.
func NewSQLStore(db storage.Database) *SQLStore { return &SQLStore{db: db} }

// Get returns the current state or an explicitly empty initial state.
func (s *SQLStore) Read(ctx context.Context) (model.Runtime, error) {
	query, args, err := s.db.Dialect().From("gitops_runtime").Prepared(true).
		Select("payload").Where(goqu.Ex{"id": 1}).ToSQL()
	if err != nil {
		return model.Runtime{}, err
	}
	var payload []byte
	err = s.db.Get(ctx, &payload, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Runtime{Stacks: map[string]model.Stack{}, Networks: map[string]model.Network{}}, nil
	}
	if err != nil {
		return model.Runtime{}, err
	}
	var state model.Runtime
	err = json.Unmarshal(payload, &state)
	return state, err
}

// Update atomically mutates state and propagates SQL and commit failures.
func (s *SQLStore) Update(ctx context.Context, fn func(*model.Runtime)) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		state, err := s.Read(ctx)
		if err != nil {
			return err
		}
		fn(&state)
		return s.Save(ctx, state)
	})
}

// Save replaces the runtime snapshot, including during the initial import.
func (s *SQLStore) Save(ctx context.Context, state model.Runtime) error {
	if state.Stacks == nil {
		state.Stacks = map[string]model.Stack{}
	}
	if state.Networks == nil {
		state.Networks = map[string]model.Network{}
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	query, args, err := s.db.Dialect().Insert("gitops_runtime").Prepared(true).
		Rows(goqu.Record{"id": 1, "payload": string(payload)}).
		OnConflict(goqu.DoUpdate("id", goqu.Record{"payload": goqu.L("excluded.payload")})).
		ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}
