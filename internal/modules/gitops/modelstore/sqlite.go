package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists runtime state synchronously in the caller's transaction.
type SQLStore struct{ db *storage.Database }

// NewSQLStore creates a runtime repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// Get returns the current state or an explicitly empty initial state.
func (s *SQLStore) Read(ctx context.Context) (model.Runtime, error) {
	state, err := storage.GetJSON[model.Runtime](ctx, s.db.Get, "SELECT payload FROM gitops_runtime WHERE id=1")
	if errors.Is(err, sql.ErrNoRows) {
		return model.Runtime{Stacks: map[string]model.Stack{}, Networks: map[string]model.Network{}}, nil
	}
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
	_, err = s.db.Get(ctx).ExecContext(ctx, `INSERT INTO gitops_runtime VALUES (1,?)
		ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, string(payload))
	return err
}
