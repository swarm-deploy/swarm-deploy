package modelstore

import (
	"context"
	"encoding/json"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists recommendations while preserving existing occurrence timestamps.
type SQLStore struct{ db *storage.Database }

// NewSQLStore creates a recommendation repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List filters recommendations in their persisted sequence.
func (s *SQLStore) List(ctx context.Context, filter ListFilter) ([]model.Recommendation, error) {
	dataset := goqu.Dialect("sqlite3").From("recommendations").Prepared(true).Select("payload")
	if filter.Stack != "" {
		dataset = dataset.Where(goqu.Ex{"stack": filter.Stack})
	}
	dataset = dataset.Order(goqu.I("sequence").Asc())
	if filter.Limit > 0 {
		dataset = dataset.Limit(uint(filter.Limit))
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Recommendation{}
	for rows.Next() {
		var payload []byte
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		var recommendation model.Recommendation
		if err = json.Unmarshal(payload, &recommendation); err != nil {
			return nil, err
		}
		result = append(result, recommendation)
	}
	return result, rows.Err()
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
		if _, err = s.db.Exec(ctx, "DELETE FROM recommendations WHERE stack=?", stack); err != nil {
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
			if _, err = s.db.Exec(ctx, `INSERT INTO recommendations(id,stack,payload)
				VALUES (?,?,?) ON CONFLICT(id) DO NOTHING`, r.ID(), stack, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}
