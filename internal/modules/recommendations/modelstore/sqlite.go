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

// recommendationRow mirrors the List projection for sqlx scanning.
type recommendationRow struct {
	Severity       string `db:"severity"`
	Type           string `db:"type"`
	Stack          string `db:"stack"`
	Service        string `db:"service"`
	SourceFile     string `db:"source_file"`
	SourceDigest   string `db:"source_digest"`
	SourceCommit   string `db:"source_commit"`
	Title          string `db:"title"`
	Recommendation string `db:"recommendation"`
	CreatedAt      string `db:"created_at"`
}

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
	var rows []recommendationRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	result := make([]model.Recommendation, 0, len(rows))
	for _, row := range rows {
		r := model.Recommendation{
			Severity:       model.Severity(row.Severity),
			Type:           model.Type(row.Type),
			Title:          row.Title,
			Recommendation: row.Recommendation,
		}
		r.Subject.Stack, r.Subject.Service = row.Stack, row.Service
		r.Source.File, r.Source.Digest, r.Source.Commit = row.SourceFile, row.SourceDigest, row.SourceCommit
		if r.CreatedAt, err = time.Parse(time.RFC3339Nano, row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
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
			if existing, ok := byKey[keyOf(r)]; ok {
				r = existing
			}
			// A single composite UNIQUE(stack,service,source_file,type) constraint is the
			// only non-rowid conflict source here, so an untargeted DO NOTHING is safe.
			insertQuery, insertArgs, insertErr := goqu.Dialect("sqlite3").Insert("recommendations").Prepared(true).
				Rows(goqu.Record{
					"severity": string(r.Severity), "type": string(r.Type), "stack": stack,
					"service": r.Subject.Service, "source_file": r.Source.File,
					"source_digest": r.Source.Digest, "source_commit": r.Source.Commit,
					"title": r.Title, "recommendation": r.Recommendation,
					"created_at": r.CreatedAt.Format(time.RFC3339Nano),
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
