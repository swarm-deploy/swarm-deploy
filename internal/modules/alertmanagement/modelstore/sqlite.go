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
	payload, err := json.Marshal(alert)
	if err != nil {
		return err
	}
	var resolved any
	if alert.ResolvedAt != nil {
		resolved = alert.ResolvedAt.UnixNano()
	}
	var deploymentID any
	if alert.DeploymentID != "" {
		deploymentID = alert.DeploymentID
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query := `INSERT INTO alerts
			(fingerprint,kind,resource_type,resource_id,deployment_id,status,opened_at_ns,updated_at_ns,
			 resolved_at_ns,payload,id)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`
		if update {
			query = `UPDATE alerts SET fingerprint=?,kind=?,resource_type=?,resource_id=?,deployment_id=?,status=?,
				opened_at_ns=?,updated_at_ns=?,resolved_at_ns=?,payload=? WHERE id=?`
		}
		result, writeErr := s.db.Get(ctx).ExecContext(ctx, query,
			alert.Fingerprint, alert.Kind, alert.ResourceType, alert.ResourceID, deploymentID, alert.Status,
			alert.OpenedAt.UnixNano(), alert.UpdatedAt.UnixNano(), resolved, string(payload), alert.ID)
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
			return ErrAlertNotFound
		}
		_, writeErr = s.db.Get(ctx).ExecContext(ctx, `DELETE FROM alerts WHERE id IN (
			SELECT id FROM alerts WHERE status='resolved' ORDER BY resolved_at_ns,id
			LIMIT max((SELECT count(*) FROM alerts)-?,0))`, MaxStoredAlerts)
		return writeErr
	})
}

// Get returns an incident by ID.
func (s *SQLStore) Get(ctx context.Context, id string) (model.Alert, error) {
	return s.get(ctx, "SELECT payload FROM alerts WHERE id=?", id)
}

// FindOpenByFingerprint resolves the active incident for a resource.
func (s *SQLStore) FindOpenByFingerprint(ctx context.Context, fingerprint string) (model.Alert, error) {
	return s.get(ctx, "SELECT payload FROM alerts WHERE fingerprint=? AND status='open'", fingerprint)
}

func (s *SQLStore) get(ctx context.Context, query, key string) (model.Alert, error) {
	alert, err := storage.GetJSON[model.Alert](ctx, s.db.Get, query, key)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Alert{}, ErrAlertNotFound
	}
	return alert, err
}

// List returns incidents ordered by most recent lifecycle update.
func (s *SQLStore) List(ctx context.Context, filter ListFilter) ([]model.Alert, error) {
	dataset := goqu.Dialect("sqlite3").From("alerts").Prepared(true).Select("payload")
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
	dataset = dataset.Order(goqu.I("updated_at_ns").Desc(), goqu.I("id").Asc())
	if filter.Limit > 0 {
		dataset = dataset.Limit(uint(filter.Limit))
	}
	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, err
	}
	return storage.QueryJSON[model.Alert](ctx, s.db.Get, query, args...)
}

// Consume executes an idempotent projection and its source marker atomically.
// When called by Outbox, its savepoint joins the worker's T3 acknowledgement.
func (s *SQLStore) Consume(ctx context.Context, id string, apply func(context.Context) error) error {
	if id == "" {
		return fmt.Errorf("alert projection requires source event ID")
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		result, err := s.db.Get(ctx).ExecContext(ctx, "INSERT INTO alert_events VALUES (?) ON CONFLICT DO NOTHING", id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		return apply(ctx)
	})
}

// ConsumeLatest ignores delayed events older than the last applied resource transition.
func (s *SQLStore) ConsumeLatest(
	ctx context.Context, id, resource string, at time.Time, apply func(context.Context) error,
) error {
	return s.Consume(ctx, id, func(ctx context.Context) error {
		accepted, err := storage.AdvanceProjection(ctx, s.db.Get, "alerts", resource, at, id)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		return apply(ctx)
	})
}
