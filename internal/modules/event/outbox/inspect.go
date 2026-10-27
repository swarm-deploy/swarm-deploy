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

// ListDeliveriesFilter restricts retained outbox deliveries.
type ListDeliveriesFilter struct {
	// SubscriptionIDPrefix restricts results to subscription IDs with this prefix.
	SubscriptionIDPrefix string
	// Statuses restricts results to the supplied delivery states; empty means all states.
	Statuses []DeliveryStatus
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

// ListDeliveries returns retained deliveries matching filter.
func (r *Repository) ListDeliveries(ctx context.Context, filter ListDeliveriesFilter) ([]Delivery, error) {
	dataset := goqu.Dialect("sqlite3").From(goqu.T("outbox_deliveries").As("d")).Prepared(true).
		Join(goqu.T("outbox_events").As("e"), goqu.On(goqu.I("e.id").Eq(goqu.I("d.event_id")))).
		Select(
			goqu.I("d.event_id"), goqu.I("d.subscription_id"), goqu.I("e.event_type"),
			goqu.I("d.status"), goqu.I("d.attempts"),
			goqu.COALESCE(goqu.I("d.last_error"), "").As("last_error"),
		).
		Order(goqu.I("d.updated_at").Asc(), goqu.I("d.event_id").Asc(), goqu.I("d.subscription_id").Asc())
	if filter.SubscriptionIDPrefix != "" {
		dataset = dataset.Where(goqu.L(
			"substr(d.subscription_id, 1, ?) = ?", len(filter.SubscriptionIDPrefix), filter.SubscriptionIDPrefix,
		))
	}
	if len(filter.Statuses) > 0 {
		statuses := make([]any, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			statuses = append(statuses, status)
		}
		dataset = dataset.Where(goqu.I("d.status").In(statuses...))
	}

	query, args, err := dataset.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	deliveries := []Delivery{}
	if err = r.db.Select(ctx, &deliveries, query, args...); err != nil {
		return nil, err
	}
	return deliveries, nil
}
