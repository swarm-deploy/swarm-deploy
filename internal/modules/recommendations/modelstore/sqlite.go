package modelstore

import (
	"context"
	"encoding/json"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists recommendations while preserving existing occurrence timestamps.
type SQLStore struct{ db *storage.Database }

// NewSQLStore creates a recommendation repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// Import preserves legacy recommendation order and occurrence timestamps.
func (s *SQLStore) Import(ctx context.Context, recommendations []model.Recommendation) error {
	for _, r := range recommendations {
		payload, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = s.db.Get(ctx).ExecContext(ctx, "INSERT INTO recommendations(id,stack,payload) VALUES (?,?,?)",
			r.ID(), r.Subject.Stack, string(payload)); err != nil {
			return err
		}
	}
	return nil
}

// List filters recommendations in their persisted sequence.
func (s *SQLStore) List(ctx context.Context, filter ListFilter) ([]model.Recommendation, error) {
	query := "SELECT payload FROM recommendations WHERE 1=1"
	args := []any{}
	if filter.Stack != "" {
		query += " AND stack=?"
		args = append(args, filter.Stack)
	}
	query += " ORDER BY sequence"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	return storage.QueryJSON[model.Recommendation](ctx, s.db.Get, query, args...)
}

// UpdateStack atomically replaces a stack's recommendations.
func (s *SQLStore) UpdateStack(ctx context.Context, stack string, recommendations []model.Recommendation) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		previous, err := s.List(ctx, ListFilter{Stack: stack})
		if err != nil {
			return err
		}
		byID := map[string]model.Recommendation{}
		for _, r := range previous {
			byID[r.ID()] = r
		}
		if _, err = s.db.Get(ctx).ExecContext(ctx, "DELETE FROM recommendations WHERE stack=?", stack); err != nil {
			return err
		}
		for _, r := range recommendations {
			r.Subject.Stack = stack
			if existing, ok := byID[r.ID()]; ok {
				r = existing
			}
			payload, encodeErr := json.Marshal(r)
			if encodeErr != nil {
				return encodeErr
			}
			if _, err = s.db.Get(ctx).ExecContext(ctx, `INSERT INTO recommendations(id,stack,payload)
				VALUES (?,?,?) ON CONFLICT(id) DO NOTHING`, r.ID(), stack, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}
