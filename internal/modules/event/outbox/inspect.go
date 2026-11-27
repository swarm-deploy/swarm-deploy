package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

const alertIDPayloadPath = "$.alert.id"

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
	dataset := r.db.Dialect().From("outbox_events").Prepared(true).
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
	dataset := r.db.Dialect().From(goqu.T("outbox_deliveries").As("d")).Prepared(true).
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

// AlertOpeningRecipients returns the recipients from a retained AlertOpened publication.
func (r *Repository) AlertOpeningRecipients(
	ctx context.Context, alertID string,
) ([]events.AlertRecipient, bool, error) {
	var row publicationRow
	query, args, err := r.db.Dialect().From("outbox_events").Prepared(true).
		Select("id", "event_type", "schema_version", "payload", "occurred_at", "sequence", "traceparent").
		Where(
			goqu.Ex{"event_type": events.TypeNameAlertOpened},
			goqu.Func("json_extract", goqu.I("payload"), alertIDPayloadPath).Eq(alertID),
		).
		Order(goqu.I("sequence").Asc()).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, false, fmt.Errorf("build alert opening query: %w", err)
	}
	if err = r.db.Get(ctx, &row, query, args...); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	publication, err := row.publication()
	if err != nil {
		return nil, false, err
	}
	event, ok := publication.Envelope.Event.(*events.AlertOpened)
	if !ok {
		return nil, false, errors.Join(ErrInvalidPayload, fmt.Errorf(
			"expected AlertOpened publication, got %T", publication.Envelope.Event,
		))
	}
	return event.Recipients, true, nil
}

// RetainedAlertLifecycleIDs returns alert IDs that still have an AlertOpened or AlertResolved publication.
func (r *Repository) RetainedAlertLifecycleIDs(
	ctx context.Context, alertIDs []string,
) (map[string]bool, error) {
	retained := make(map[string]bool)
	if len(alertIDs) == 0 {
		return retained, nil
	}
	// A retained publication protects its link even if its schema cannot be decoded.
	alertID := goqu.Func("json_extract", goqu.I("payload"), alertIDPayloadPath)
	query, args, err := r.db.Dialect().From("outbox_events").Prepared(true).
		Select(alertID).Distinct().
		Where(
			goqu.I("event_type").In(events.TypeNameAlertOpened, events.TypeNameAlertResolved),
			alertID.In(alertIDs),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build retained alert lifecycle query: %w", err)
	}
	var retainedIDs []string
	if err = r.db.Select(ctx, &retainedIDs, query, args...); err != nil {
		return nil, err
	}
	for _, id := range retainedIDs {
		retained[id] = true
	}
	return retained, nil
}
