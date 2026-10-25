package modelstore

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// SQLStore persists recommendations while preserving existing occurrence timestamps.
type SQLStore struct{ db *storage.Database }

// recommendationKey mirrors UNIQUE(stack, service, source_file, type) of the recommendations table.
type recommendationKey struct {
	stack, service, sourceFile string
	typ                        model.Type
}

func keyOf(r model.Recommendation) recommendationKey {
	return recommendationKey{r.Subject.Stack, r.Subject.Service, r.Source.File, r.Type}
}

// NewSQLStore creates a recommendation repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// List filters recommendations in their persisted sequence.
func (s *SQLStore) List(ctx context.Context, filter ListFilter) ([]model.Recommendation, error) {
	dataset := goqu.Dialect("sqlite3").From("recommendations").Prepared(true).Select(
		"severity", "type",
		goqu.I("stack").As(goqu.C("subject.stack")),
		goqu.I("service").As(goqu.C("subject.service")),
		goqu.I("source_file").As(goqu.C("source.file")),
		goqu.I("source_digest").As(goqu.C("source.digest")),
		goqu.I("source_commit").As(goqu.C("source.commit")),
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
	result := []model.Recommendation{}
	if err = s.db.Select(ctx, &result, query, args...); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateStack atomically replaces a stack's recommendations.
func (s *SQLStore) UpdateStack(ctx context.Context, stack string, recommendations []model.Recommendation) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		previous, err := s.List(ctx, ListFilter{Stack: stack})
		if err != nil {
			return err
		}
		byKey := map[recommendationKey]model.Recommendation{}
		for _, r := range previous {
			byKey[keyOf(r)] = r
		}
		deleteQuery, deleteArgs, err := goqu.Dialect("sqlite3").Delete("recommendations").Prepared(true).
			Where(goqu.Ex{"stack": stack}).ToSQL()
		if err != nil {
			return err
		}
		if _, err = s.db.Exec(ctx, deleteQuery, deleteArgs...); err != nil {
			return err
		}
		for _, r := range recommendations {
			r.Subject.Stack = stack
			r, err = recommendationForStorage(r, byKey)
			if err != nil {
				return err
			}
			// A single composite UNIQUE(stack,service,source_file,type) constraint is the
			// only non-rowid conflict source here, so an untargeted DO NOTHING is safe.
			insertQuery, insertArgs, insertErr := goqu.Dialect("sqlite3").Insert("recommendations").Prepared(true).
				Rows(goqu.Record{
					"severity": string(r.Severity), "type": string(r.Type), "stack": stack,
					"service": r.Subject.Service, "source_file": r.Source.File,
					"source_digest": r.Source.Digest, "source_commit": r.Source.Commit,
					"title": r.Title, "recommendation": r.Recommendation,
					"created_at": r.CreatedAt.UTC(),
				}).
				OnConflict(goqu.DoNothing()).ToSQL()
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

func recommendationForStorage(
	r model.Recommendation, previous map[recommendationKey]model.Recommendation,
) (model.Recommendation, error) {
	if existing, ok := previous[keyOf(r)]; ok {
		r.CreatedAt = existing.CreatedAt
	}
	if r.CreatedAt.IsZero() {
		return model.Recommendation{}, fmt.Errorf("recommendation creation time is required")
	}
	return r, nil
}
