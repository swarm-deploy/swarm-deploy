// Package outbox persists durable asynchronous event deliveries in SQLite.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type causeKey struct{}

const (
	databaseOperationTimeout = 5 * time.Second
)

// Repository persists publications and delivery state in SQLite.
type Repository struct {
	db *storage.Database
}

// New constructs an outbox repository.
func New(db *storage.Database) *Repository {
	return &Repository{db: db}
}

// Publish enqueues the event for the supplied subscriptions. With an active transaction it joins the publisher's T1
// (or a subscriber's T3); otherwise it opens a short insertion transaction.
func (r *Repository) Publish(ctx context.Context, event events.Event, subscriptionIDs []string, now time.Time) error {
	if event == nil {
		return errors.New("cannot publish a nil event")
	}
	if len(subscriptionIDs) == 0 {
		return nil
	}
	payload, err := codec.Encode(event)
	if err != nil {
		return err
	}
	eventID, idErr := uuid.NewV7()
	if idErr != nil {
		return idErr
	}
	id, now := eventID.String(), now.UTC()
	if now.IsZero() {
		return errors.New("outbox publication time is required")
	}
	traceparent := traceparentFromContext(ctx)
	insert := func(ctx context.Context) error {
		cause, _ := ctx.Value(causeKey{}).(string)
		query, args, buildErr := goqu.Dialect("sqlite3").Insert("outbox_events").Prepared(true).
			Rows(goqu.Record{
				"id": id, "event_type": event.Type().Name(), "schema_version": codec.Version,
				"occurred_at": now, "causation_id": cause, "payload": string(payload),
				"traceparent": traceparent,
			}).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		_, insertErr := r.db.Exec(ctx, query, args...)
		if insertErr != nil {
			return fmt.Errorf("insert outbox event: %w", insertErr)
		}
		for _, subscriber := range subscriptionIDs {
			query, args, buildErr = goqu.Dialect("sqlite3").Insert("outbox_deliveries").Prepared(true).
				Rows(goqu.Record{
					"event_id": id, "subscription_id": subscriber,
					"available_at": now, "updated_at": now,
				}).ToSQL()
			if buildErr != nil {
				return buildErr
			}
			_, insertErr = r.db.Exec(ctx, query, args...)
			if insertErr != nil {
				return fmt.Errorf("insert outbox delivery: %w", insertErr)
			}
		}
		return nil
	}
	return r.db.WithinTransaction(ctx, insert)
}

func traceparentFromContext(ctx context.Context) any {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return nil
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	if traceparent := carrier.Get("traceparent"); traceparent != "" {
		return traceparent
	}
	return nil
}
