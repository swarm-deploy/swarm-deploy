package modelstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"modernc.org/sqlite"
	sqlitecode "modernc.org/sqlite/lib"
)

var alertColumns = []any{
	"id", "fingerprint", "kind", "resource_type", "resource_id", "deployment_id", "status",
	"opened_at", "updated_at", "resolved_at", "payload",
}

var dialect = goqu.Dialect("sqlite3")

// SQLStore persists alert lifecycle independently of the delivery queue.
type SQLStore struct{ db *storage.Database }

// NewSQLStore creates an alert repository.
func NewSQLStore(db *storage.Database) *SQLStore { return &SQLStore{db: db} }

// Create inserts an incident with database-enforced open-fingerprint uniqueness.
func (s *SQLStore) Create(ctx context.Context, alert model.Alert) error {
	return s.write(ctx, alert, false)
}

// Update replaces an existing incident.
func (s *SQLStore) Update(ctx context.Context, alert model.Alert) error {
	return s.write(ctx, alert, true)
}

func (s *SQLStore) write(ctx context.Context, alert model.Alert, update bool) error {
	payload, err := json.Marshal(payloadFromAlert(alert))
	if err != nil {
		return err
	}
	resolved, err := alertResolvedAt(alert)
	if err != nil {
		return err
	}
	deploymentID := sql.NullString{}
	if alert.DeploymentID != "" {
		deploymentID = sql.NullString{String: alert.DeploymentID, Valid: true}
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, buildErr := alertWriteStatement(alert, deploymentID, resolved, string(payload), update)
		if buildErr != nil {
			return buildErr
		}
		result, writeErr := s.db.Exec(ctx, query, args...)
		if writeErr != nil {
			var constraint *sqlite.Error
			if errors.As(writeErr, &constraint) && constraint.Code() == sqlitecode.SQLITE_CONSTRAINT_UNIQUE {
				return ErrOpenAlertExists
			}
			return writeErr
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			return countErr
		}
		if count != 1 {
			return s.alertWriteMiss(ctx, alert.ID, update)
		}
		alertsCount := dialect.From("alerts").Prepared(true).Select(goqu.COUNT("*"))
		rankedResolved := dialect.From("alerts").Prepared(true).Select(
			"id",
			goqu.L("row_number() OVER (ORDER BY resolved_at,id)").As("prune_rank"),
		).
			Where(goqu.Ex{"status": "resolved"}).
			Order(goqu.I("resolved_at").Asc(), goqu.I("id").Asc())
		pruned := dialect.From(rankedResolved.As("resolved")).Prepared(true).
			Select("id").
			Where(goqu.C("prune_rank").Lte(goqu.L("max((?)-?,0)", alertsCount, MaxStoredAlerts)))
		query, args, buildErr = dialect.Delete("alerts").Prepared(true).
			Where(goqu.C("id").In(pruned)).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		_, writeErr = s.db.Exec(ctx, query, args...)
		return writeErr
	})
}

func alertResolvedAt(alert model.Alert) (sql.NullTime, error) {
	if alert.OpenedAt.IsZero() || alert.UpdatedAt.IsZero() {
		return sql.NullTime{}, fmt.Errorf("alert timestamps are required")
	}
	if alert.ResolvedAt == nil {
		return sql.NullTime{}, nil
	}
	if alert.ResolvedAt.IsZero() {
		return sql.NullTime{}, fmt.Errorf("resolved alert timestamp is required")
	}
	return sql.NullTime{Time: alert.ResolvedAt.UTC(), Valid: true}, nil
}

func alertWriteStatement(
	alert model.Alert, deploymentID sql.NullString, resolved sql.NullTime, payload string, update bool,
) (string, []any, error) {
	if update {
		record := goqu.Record{
			"fingerprint": alert.Fingerprint, "kind": alert.Kind, "resource_type": alert.ResourceType,
			"resource_id": alert.ResourceID, "deployment_id": deploymentID, "status": alert.Status,
			"updated_at": alert.UpdatedAt.UTC(), "resolved_at": resolved, "payload": payload,
		}
		return dialect.Update("alerts").Prepared(true).Set(record).
			Where(goqu.Ex{"id": alert.ID, "opened_at": alert.OpenedAt.UTC()}).ToSQL()
	}
	record := goqu.Record{
		"id": alert.ID, "fingerprint": alert.Fingerprint, "kind": alert.Kind, "resource_type": alert.ResourceType,
		"resource_id": alert.ResourceID, "deployment_id": deploymentID, "status": alert.Status,
		"opened_at": alert.OpenedAt.UTC(), "updated_at": alert.UpdatedAt.UTC(),
		"resolved_at": resolved, "payload": payload,
	}
	return dialect.Insert("alerts").Prepared(true).Rows(record).ToSQL()
}

func (s *SQLStore) alertWriteMiss(ctx context.Context, id string, update bool) error {
	if !update {
		return ErrAlertNotFound
	}
	var openedAt time.Time
	query, args, err := dialect.From("alerts").Prepared(true).
		Select("opened_at").Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return err
	}
	err = s.db.Get(ctx, &openedAt, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAlertNotFound
	}
	if err != nil {
		return err
	}
	return ErrAlertOpenedAtChanged
}

// Get returns an incident by ID.
func (s *SQLStore) Get(ctx context.Context, id string) (model.Alert, error) {
	query, args, err := dialect.From("alerts").Prepared(true).Select(alertColumns...).
		Where(goqu.Ex{"id": id}).ToSQL()
	if err != nil {
		return model.Alert{}, err
	}
	return s.get(ctx, query, args...)
}

// FindOpenByFingerprint resolves the active incident for a resource.
func (s *SQLStore) FindOpenByFingerprint(ctx context.Context, fingerprint string) (model.Alert, error) {
	query, args, err := dialect.From("alerts").Prepared(true).Select(alertColumns...).
		Where(goqu.Ex{"fingerprint": fingerprint, "status": "open"}).ToSQL()
	if err != nil {
		return model.Alert{}, err
	}
	return s.get(ctx, query, args...)
}

func (s *SQLStore) get(ctx context.Context, query string, args ...any) (model.Alert, error) {
	var row alertRow
	err := s.db.Get(ctx, &row, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Alert{}, ErrAlertNotFound
	}
	if err != nil {
		return model.Alert{}, err
	}
	return row.toAlert()
}

// List returns incidents ordered by most recent lifecycle update.
func (s *SQLStore) List(ctx context.Context, filter ListFilter) ([]model.Alert, error) {
	dataset := dialect.From("alerts").Prepared(true).Select(alertColumns...)
	if filter.Status != "" {
		dataset = dataset.Where(goqu.Ex{"status": filter.Status})
	}
	if filter.Kind != "" {
		dataset = dataset.Where(goqu.Ex{"kind": filter.Kind})
	}
	if filter.ResourceType != "" {
		dataset = dataset.Where(goqu.Ex{"resource_type": filter.ResourceType})
	}
	if filter.ResourceID != "" {
		dataset = dataset.Where(goqu.Ex{"resource_id": filter.ResourceID})
	}
	if filter.DeploymentID != "" {
		dataset = dataset.Where(goqu.Ex{"deployment_id": filter.DeploymentID})
	}
	dataset = dataset.Order(goqu.I("updated_at").Desc(), goqu.I("id").Asc())
	if filter.Limit > 0 {
		dataset = dataset.Limit(uint(filter.Limit))
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	var rows []alertRow
	if err = s.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	alerts := make([]model.Alert, 0, len(rows))
	for _, row := range rows {
		alert, convertErr := row.toAlert()
		if convertErr != nil {
			return nil, convertErr
		}
		alerts = append(alerts, alert)
	}
	return alerts, nil
}

// IncrementFailureStreak records another consecutive failure and returns the new count.
func (s *SQLStore) IncrementFailureStreak(ctx context.Context, fingerprint string) (uint64, error) {
	var failures uint64
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, buildErr := dialect.Insert("alert_failure_streaks").Prepared(true).
			Rows(goqu.Record{"fingerprint": fingerprint, "failures": 1}).
			OnConflict(goqu.DoUpdate("fingerprint", goqu.Record{"failures": goqu.L("failures+1")})).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		if _, execErr := s.db.Exec(ctx, query, args...); execErr != nil {
			return execErr
		}
		query, args, buildErr = dialect.From("alert_failure_streaks").Prepared(true).
			Select("failures").Where(goqu.Ex{"fingerprint": fingerprint}).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		return s.db.Get(ctx, &failures, query, args...)
	})
	return failures, err
}

// ResetFailureStreak clears consecutive failures after recovery.
func (s *SQLStore) ResetFailureStreak(ctx context.Context, fingerprint string) error {
	query, args, err := dialect.Delete("alert_failure_streaks").Prepared(true).
		Where(goqu.Ex{"fingerprint": fingerprint}).ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// ResetFailureStreaksExcept clears stack counters for resources absent from the desired configuration.
func (s *SQLStore) ResetFailureStreaksExcept(ctx context.Context, fingerprints []string) error {
	dataset := dialect.Delete("alert_failure_streaks").Prepared(true).
		Where(goqu.C("fingerprint").Like(string(model.AlertKindSyncFailed) + ":" +
			string(model.ResourceTypeStack) + ":%"))
	if len(fingerprints) > 0 {
		dataset = dataset.Where(goqu.I("fingerprint").NotIn(fingerprints))
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, query, args...)
	return err
}

// ConsumeLatest ignores delayed events older than the last applied resource transition.
func (s *SQLStore) ConsumeLatest(
	ctx context.Context, id, resource string, at time.Time, apply func(context.Context) error,
) error {
	if id == "" || at.IsZero() {
		return fmt.Errorf("alert projection requires source event ID and timestamp")
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		accepted, err := storage.AdvanceProjection(ctx, s.db, "alerts", resource, at, id)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		return apply(ctx)
	})
}

