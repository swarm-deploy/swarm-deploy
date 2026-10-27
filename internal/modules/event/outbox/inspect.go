package outbox

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// ListFilter restricts retained outbox publications.
type ListFilter struct {
	// EventType restricts results to one event type; empty means all event types.
	EventType events.TypeName
}

// ListPublications returns retained publications in durable publication order.
func (r *Repository) ListPublications(ctx context.Context, filter ListFilter) ([]Publication, error) {
	dataset := goqu.Dialect("sqlite3").From("outbox_events").Prepared(true).
		Select("id", "event_type", "schema_version", "payload", "occurred_at", "sequence", "traceparent").
		Order(goqu.I("sequence").Asc())
	if filter.EventType != "" {
		dataset = dataset.Where(goqu.Ex{"event_type": filter.EventType})
	}

	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows := []publicationRow{}
	if err = r.db.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}

	publications := make([]Publication, 0, len(rows))
	for _, row := range rows {
		publication, decodeErr := row.publication()
		if decodeErr != nil {
			return nil, decodeErr
		}
		publications = append(publications, publication)
	}

	return publications, nil
}

// FailedDelivery exposes operational metadata, never serialized event contents.
type FailedDelivery struct {
	// EventID identifies the retained publication.
	EventID string `json:"event_id" db:"event_id"`
	// SubscriptionID identifies the failed destination.
	SubscriptionID string `json:"subscription_id" db:"subscription_id"`
	// EventType is the safe domain type.
	EventType string `json:"event_type" db:"event_type"`
	// Attempts counts processing claims.
	Attempts int `json:"attempts" db:"attempts"`
	// LastError contains a stable error category.
	LastError string `json:"last_error" db:"last_error"`
}

// ListFailedDeliveries returns retained terminal failures for explicit operator action.
func (r *Repository) ListFailedDeliveries(ctx context.Context) ([]FailedDelivery, error) {
	query, args, err := goqu.Dialect("sqlite3").
		From(goqu.T("outbox_deliveries").As("d")).Prepared(true).
		Join(goqu.T("outbox_events").As("e"), goqu.On(goqu.I("e.id").Eq(goqu.I("d.event_id")))).
		Select(
			goqu.I("d.event_id"), goqu.I("d.subscription_id"), goqu.I("e.event_type"),
			goqu.I("d.attempts"), goqu.COALESCE(goqu.I("d.last_error"), "").As("last_error"),
		).
		Where(goqu.I("d.status").Eq("failed")).
		Order(goqu.I("d.updated_at").Asc(), goqu.I("d.event_id").Asc(), goqu.I("d.subscription_id").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	result := []FailedDelivery{}
	err = r.db.Select(ctx, &result, query, args...)

	return result, err
}
