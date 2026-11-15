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
	StackName   string       `db:"stack_name"`
	ServiceName string       `db:"service_name"`
	ExpiresAt   sql.NullTime `db:"expires_at"`
}

// Repository persists direct pause records in GitOps-owned tables.
type Repository struct{ db storage.Database }

// NewRepository creates a pause repository.
func NewRepository(db storage.Database) *Repository { return &Repository{db: db} }

// Active returns active direct pauses for one stack at now.
func (r *Repository) Active(ctx context.Context, stack string, now time.Time) (ActivePolicy, error) {
	policy := ActivePolicy{ServicePauses: map[string]Pause{}}
	stackPause, found, err := r.stack(ctx, stack, now, true)
	if err != nil {
		return ActivePolicy{}, err
	}
	if found {
		policy.StackPause = &stackPause
	}
	query, args, err := r.db.Dialect().From("gitops_service_sync_pauses").Prepared(true).
		Select("stack_name", "service_name", "expires_at").
		Where(goqu.Ex{"stack_name": stack}, activeExpression(now)).
		Order(goqu.I("service_name").Asc()).ToSQL()
	if err != nil {
		return ActivePolicy{}, err
	}
	var rows []pauseRow
	if err = r.db.Select(ctx, &rows, query, args...); err != nil {
		return ActivePolicy{}, err
	}
	for _, row := range rows {
		pause := row.pause(ScopeService)
		policy.ServicePauses[pause.ServiceName] = pause
	}
	return policy, nil
}

// Direct returns a direct pause even when it is expired.
func (r *Repository) Direct(ctx context.Context, scope Scope, stack, service string) (Pause, bool, error) {
	if scope == ScopeStack {
		return r.stack(ctx, stack, time.Time{}, false)
	}
	query, args, err := r.db.Dialect().From("gitops_service_sync_pauses").Prepared(true).
		Select("stack_name", "service_name", "expires_at").
		Where(goqu.Ex{"stack_name": stack, "service_name": service}).ToSQL()
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
	return row.pause(ScopeService), true, nil
}

func (r *Repository) stack(
	ctx context.Context, stack string, now time.Time, activeOnly bool,
) (Pause, bool, error) {
	dataset := r.db.Dialect().From("gitops_stack_sync_pauses").Prepared(true).
		Select("stack_name", goqu.V("").As("service_name"), "expires_at").Where(goqu.Ex{"stack_name": stack})
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
	return row.pause(ScopeStack), true, nil
}

// Upsert stores a direct pause and reports whether its expiration changed.
func (r *Repository) Upsert(ctx context.Context, pause Pause) (bool, error) {
	existing, found, err := r.Direct(ctx, pause.Scope, pause.StackName, pause.ServiceName)
	if err != nil {
		return false, err
	}
	if found && equalExpiration(existing.ExpiresAt, pause.ExpiresAt) {
		return false, nil
	}
	table := "gitops_stack_sync_pauses"
	record := goqu.Record{"stack_name": pause.StackName, "expires_at": nullableTime(pause.ExpiresAt)}
	where := goqu.Ex{"stack_name": pause.StackName}
	if pause.Scope == ScopeService {
		table = "gitops_service_sync_pauses"
		record["service_name"] = pause.ServiceName
		where["service_name"] = pause.ServiceName
	}
	var query string
	var args []any
	if found {
		query, args, err = r.db.Dialect().Update(table).Prepared(true).
			Set(goqu.Record{"expires_at": nullableTime(pause.ExpiresAt)}).Where(where).ToSQL()
	} else {
		query, args, err = r.db.Dialect().Insert(table).Prepared(true).Rows(record).ToSQL()
	}
	if err != nil {
		return false, err
	}
	_, err = r.db.Exec(ctx, query, args...)
	return err == nil, err
}

// Delete removes one direct pause and reports whether a row existed.
func (r *Repository) Delete(ctx context.Context, scope Scope, stack, service string) (bool, error) {
	table := "gitops_stack_sync_pauses"
	where := goqu.Ex{"stack_name": stack}
	if scope == ScopeService {
		table = "gitops_service_sync_pauses"
		where["service_name"] = service
	}
	query, args, err := r.db.Dialect().Delete(table).Prepared(true).Where(where).ToSQL()
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

// Expired returns expired pauses in deterministic order.
func (r *Repository) Expired(ctx context.Context, now time.Time) ([]Pause, error) {
	var pauses []Pause
	for _, item := range []struct {
		table string
		scope Scope
	}{{"gitops_stack_sync_pauses", ScopeStack}, {"gitops_service_sync_pauses", ScopeService}} {
		columns := []any{"stack_name", goqu.V("").As("service_name"), "expires_at"}
		if item.scope == ScopeService {
			columns[1] = "service_name"
		}
		query, args, err := r.db.Dialect().From(item.table).Prepared(true).Select(columns...).
			Where(goqu.C("expires_at").IsNotNull(), goqu.C("expires_at").Lte(now.UTC())).ToSQL()
		if err != nil {
			return nil, err
		}
		var rows []pauseRow
		if err = r.db.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, row := range rows {
			pauses = append(pauses, row.pause(item.scope))
		}
	}
	sort.Slice(pauses, func(i, j int) bool {
		if pauses[i].StackName != pauses[j].StackName {
			return pauses[i].StackName < pauses[j].StackName
		}
		if pauses[i].Scope != pauses[j].Scope {
			return pauses[i].Scope < pauses[j].Scope
		}
		return pauses[i].ServiceName < pauses[j].ServiceName
	})
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

func (r pauseRow) pause(scope Scope) Pause {
	pause := Pause{Scope: scope, StackName: r.StackName, ServiceName: r.ServiceName}
	if r.ExpiresAt.Valid {
		expires := r.ExpiresAt.Time.UTC()
		pause.ExpiresAt = &expires
	}
	return pause
}
