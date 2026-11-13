package syncpause

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

type pauseRow struct {
	StackName string       `db:"stack_name"`
	ExpiresAt sql.NullTime `db:"expires_at"`
}

// Repository persists stack pause records in GitOps-owned storage.
type Repository struct{ db storage.Database }

// NewRepository creates a pause repository.
func NewRepository(db storage.Database) *Repository { return &Repository{db: db} }

// Active returns the active stack pause at now.
func (r *Repository) Active(ctx context.Context, stack string, now time.Time) (ActivePolicy, error) {
	pause, found, err := r.stack(ctx, stack, now, true)
	if err != nil {
		return ActivePolicy{}, err
	}
	if !found {
		return ActivePolicy{}, nil
	}
	return ActivePolicy{StackPause: &pause}, nil
}

// Direct returns a stack pause even when it is expired.
func (r *Repository) Direct(ctx context.Context, stack string) (Pause, bool, error) {
	return r.stack(ctx, stack, time.Time{}, false)
}

func (r *Repository) stack(
	ctx context.Context, stack string, now time.Time, activeOnly bool,
) (Pause, bool, error) {
	dataset := r.db.Dialect().From("gitops_stack_sync_pauses").Prepared(true).
		Select("stack_name", "expires_at").Where(goqu.Ex{"stack_name": stack})
	if activeOnly {
		dataset = dataset.Where(activeExpression(now))
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return Pause{}, false, err
	}
	var row pauseRow
	err = r.db.Get(ctx, &row, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Pause{}, false, nil
	}
	if err != nil {
		return Pause{}, false, err
	}
	return row.pause(), true, nil
}

// Upsert stores a stack pause and reports whether its expiration changed.
func (r *Repository) Upsert(ctx context.Context, pause Pause) (bool, error) {
	existing, found, err := r.Direct(ctx, pause.StackName)
	if err != nil {
		return false, err
	}
	if found && equalExpiration(existing.ExpiresAt, pause.ExpiresAt) {
		return false, nil
	}
	record := goqu.Record{"stack_name": pause.StackName, "expires_at": nullableTime(pause.ExpiresAt)}
	var query string
	var args []any
	if found {
		query, args, err = r.db.Dialect().Update("gitops_stack_sync_pauses").Prepared(true).
			Set(goqu.Record{"expires_at": nullableTime(pause.ExpiresAt)}).
			Where(goqu.Ex{"stack_name": pause.StackName}).ToSQL()
	} else {
		query, args, err = r.db.Dialect().Insert("gitops_stack_sync_pauses").Prepared(true).Rows(record).ToSQL()
	}
	if err != nil {
		return false, err
	}
	_, err = r.db.Exec(ctx, query, args...)
	return err == nil, err
}

// Delete removes one stack pause and reports whether a row existed.
func (r *Repository) Delete(ctx context.Context, stack string) (bool, error) {
	query, args, err := r.db.Dialect().Delete("gitops_stack_sync_pauses").Prepared(true).
		Where(goqu.Ex{"stack_name": stack}).ToSQL()
	if err != nil {
		return false, err
	}
	result, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// Expired returns expired stack pauses in deterministic order.
func (r *Repository) Expired(ctx context.Context, now time.Time) ([]Pause, error) {
	query, args, err := r.db.Dialect().From("gitops_stack_sync_pauses").Prepared(true).
		Select("stack_name", "expires_at").
		Where(goqu.C("expires_at").IsNotNull(), goqu.C("expires_at").Lte(now.UTC())).ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []pauseRow
	if err = r.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	pauses := make([]Pause, 0, len(rows))
	for _, row := range rows {
		pauses = append(pauses, row.pause())
	}
	sort.Slice(pauses, func(i, j int) bool { return pauses[i].StackName < pauses[j].StackName })
	return pauses, nil
}

func activeExpression(now time.Time) goqu.Expression {
	return goqu.Or(goqu.C("expires_at").IsNull(), goqu.C("expires_at").Gt(now.UTC()))
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func equalExpiration(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func (r pauseRow) pause() Pause {
	pause := Pause{StackName: r.StackName}
	if r.ExpiresAt.Valid {
		expires := r.ExpiresAt.Time.UTC()
		pause.ExpiresAt = &expires
	}
	return pause
}
