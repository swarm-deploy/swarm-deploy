package modelstore

import (
	"context"
	"time"

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
	dataset := goqu.Dialect("sqlite3").From("recommendations").Prepared(true).Select(
		"severity", "type", "stack", "service", "source_file", "source_digest", "source_commit",
		"title", "recommendation", "created_at",
	)
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
		var (
			r         model.Recommendation
			createdAt string
		)
		if err = rows.Scan(
			&r.Severity, &r.Type, &r.Subject.Stack, &r.Subject.Service,
			&r.Source.File, &r.Source.Digest, &r.Source.Commit,
			&r.Title, &r.Recommendation, &createdAt,
		); err != nil {
			return nil, err
		}
		if r.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, err
		}
		result = append(result, r)
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
			if _, err = s.db.Exec(ctx, `INSERT INTO recommendations(
					id,severity,type,stack,service,source_file,source_digest,source_commit,
					title,recommendation,created_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
				r.ID(), string(r.Severity), string(r.Type), stack, r.Subject.Service,
				r.Source.File, r.Source.Digest, r.Source.Commit,
				r.Title, r.Recommendation, r.CreatedAt.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		return nil
	})
}
