package outbox

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

const (
	maxRetryBackoff    = 5 * time.Minute
	maxBackoffExponent = 9
)

// ErrLeaseLost fences a worker whose claim has expired or been replaced.
var ErrLeaseLost = errors.New("outbox lease lost")

// ErrInvalidPayload indicates an unsupported or malformed persisted event.
var ErrInvalidPayload = errors.New("unsupported or invalid outbox payload")

// Delivery identifies one claimed subscriber delivery.
type Delivery struct {
	// EventID identifies the persisted publication.
	EventID string
	// SubscriptionID identifies the destination.
	SubscriptionID string
	// Token fences concurrent or expired claims.
	Token string
	// Attempts is the number of times this delivery has been claimed.
	Attempts int
}

// Publication is a decoded event loaded from the outbox.
type Publication struct {
	// Type identifies the event kind.
	Type events.TypeName
	// Envelope contains the decoded event and durable metadata.
	Envelope events.Envelope
	// Traceparent links delivery tracing to the publishing span when present.
	Traceparent string
}

// Claim atomically leases at most one due delivery.
func (r *Repository) Claim(ctx context.Context, now time.Time, lease time.Duration) (Delivery, error) {
	d := Delivery{Token: uuid.NewString()}
	err := r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var claimed struct {
			EventID        string `db:"event_id"`
			SubscriptionID string `db:"subscription_id"`
			Attempts       int    `db:"attempts"`
		}
		query, args, buildErr := claimStatement(
			now.UTC(), now.UTC(), d.Token, now.Add(lease).UTC(), now.UTC(),
		)
		if buildErr != nil {
			return buildErr
		}
		result, err := r.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return sql.ErrNoRows
		}
		query, args, buildErr = goqu.Dialect("sqlite3").From("outbox_deliveries").Prepared(true).
			Select("event_id", "subscription_id", "attempts").
			Where(goqu.Ex{"lease_token": d.Token}).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		if err = r.db.Get(ctx, &claimed, query, args...); err != nil {
			return err
		}
		d.EventID, d.SubscriptionID, d.Attempts = claimed.EventID, claimed.SubscriptionID, claimed.Attempts
		return nil
	})
	return d, err
}

func claimStatement(
	now, expired time.Time, token string, leaseUntil, updatedAt time.Time,
) (string, []any, error) {
	dialect := goqu.Dialect("sqlite3")
	candidate := goqu.T("outbox_deliveries").As("candidate")
	candidateEvent := goqu.T("outbox_events").As("candidate_event")
	earlierEvent := goqu.T("outbox_events").As("earlier_event")
	earlierPending := dialect.From(goqu.L("outbox_deliveries AS earlier INDEXED BY idx_outbox_active_subscription")).
		Prepared(true).
		Join(earlierEvent, goqu.On(goqu.I("earlier_event.id").Eq(goqu.I("earlier.event_id")))).
		Select(goqu.L("1")).
		Where(
			goqu.I("earlier.subscription_id").Eq(goqu.I("candidate.subscription_id")),
			goqu.L("earlier.status IN ('pending','processing')"),
			goqu.I("earlier_event.sequence").Lt(goqu.I("candidate_event.sequence")),
		)
	due := dialect.From(candidate).Prepared(true).
		Join(candidateEvent, goqu.On(goqu.I("candidate_event.id").Eq(goqu.I("candidate.event_id")))).
		Select(goqu.I("candidate.event_id"), goqu.I("candidate.subscription_id")).
		Where(
			goqu.Or(
				goqu.And(goqu.I("candidate.status").Eq("pending"), goqu.I("candidate.available_at").Lte(now)),
				goqu.And(goqu.I("candidate.status").Eq("processing"), goqu.I("candidate.lease_until").Lte(expired)),
			),
			goqu.L("NOT EXISTS ?", earlierPending),
		).
		Order(
			goqu.I("candidate.available_at").Asc(),
			goqu.I("candidate_event.sequence").Asc(),
			goqu.I("candidate.subscription_id").Asc(),
		).
		Limit(1)
	return dialect.Update("outbox_deliveries").Prepared(true).
		With("due", due).
		Set(goqu.Record{
			"status":      "processing",
			"attempts":    goqu.L("attempts+1"),
			"lease_token": token,
			"lease_until": leaseUntil,
			"updated_at":  updatedAt,
		}).
		Where(goqu.L("(event_id,subscription_id) IN ?", dialect.From("due").Select("event_id", "subscription_id"))).
		ToSQL()
}

// Load decodes the publication associated with a claimed delivery.
func (r *Repository) Load(ctx context.Context, d Delivery) (Publication, error) {
	var row struct {
		EventType           events.TypeName `db:"event_type"`
		SchemaVersion       int             `db:"schema_version"`
		Payload             string          `db:"payload"`
		OccurredAt          time.Time       `db:"occurred_at"`
		PublicationSequence int64           `db:"sequence"`
		Traceparent         sql.NullString  `db:"traceparent"`
	}
	query, args, err := goqu.Dialect("sqlite3").From("outbox_events").Prepared(true).
		Select("event_type", "schema_version", "payload", "occurred_at", "sequence", "traceparent").
		Where(goqu.Ex{"id": d.EventID}).ToSQL()
	if err != nil {
		return Publication{}, err
	}
	if err = r.db.Get(ctx, &row, query, args...); err != nil {
		return Publication{}, err
	}
	publication := Publication{
		Type: row.EventType,
		Envelope: events.Envelope{
			ID: d.EventID, OccurredAt: row.OccurredAt.UTC(),
			PublicationSequence: row.PublicationSequence,
		},
		Traceparent: row.Traceparent.String,
	}
	event, err := codec.Decode(row.EventType, row.SchemaVersion, []byte(row.Payload))
	if err != nil {
		return publication, errors.Join(ErrInvalidPayload, err)
	}
	publication.Envelope.Event = event
	return publication, nil
}

// WithinTransaction executes fn in the repository transaction context.
func (r *Repository) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithinTransaction(ctx, fn)
}

// WithCause associates child publications with their source event.
func WithCause(ctx context.Context, eventID string) context.Context {
	return context.WithValue(ctx, causeKey{}, eventID)
}

// CheckLease verifies that the delivery claim is still current.
func (r *Repository) CheckLease(ctx context.Context, d Delivery, now time.Time) error {
	var count int
	query, args, err := goqu.Dialect("sqlite3").From("outbox_deliveries").Prepared(true).
		Select(goqu.COUNT("*")).
		Where(goqu.Ex{
			"event_id": d.EventID, "subscription_id": d.SubscriptionID,
			"status": "processing", "lease_token": d.Token,
		}, goqu.C("lease_until").Gt(now.UTC())).
		ToSQL()
	if err != nil {
		return err
	}
	if err = r.db.Get(ctx, &count, query, args...); err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Acknowledge marks one claimed delivery complete and removes fully delivered publications.
func (r *Repository) Acknowledge(ctx context.Context, d Delivery, now time.Time) error {
	query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
		Set(goqu.Record{
			"status": "delivered", "lease_token": nil, "lease_until": nil,
			"last_error": nil, "updated_at": now.UTC(),
		}).
		Where(goqu.Ex{
			"event_id": d.EventID, "subscription_id": d.SubscriptionID,
			"status": "processing", "lease_token": d.Token,
		}, goqu.C("lease_until").Gt(now.UTC())).
		ToSQL()
	if err != nil {
		return err
	}
	result, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if err = requireChanged(result); err != nil {
		return err
	}
	return r.deleteCompleted(ctx, d.EventID)
}

func requireChanged(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (r *Repository) deleteCompleted(ctx context.Context, eventID string) error {
	pending := goqu.Dialect("sqlite3").From("outbox_deliveries").Prepared(true).Select(goqu.L("1")).
		Where(
			goqu.I("outbox_deliveries.event_id").Eq(goqu.I("outbox_events.id")),
			goqu.I("outbox_deliveries.status").Neq("delivered"),
		)
	query, args, err := goqu.Dialect("sqlite3").Delete("outbox_events").Prepared(true).
		Where(goqu.Ex{"id": eventID}, goqu.L("NOT EXISTS ?", pending)).ToSQL()
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, query, args...)
	return err
}

// Fail releases a claimed delivery for retry or parks it as a terminal failure.
func (r *Repository) Fail(
	ctx context.Context,
	d Delivery,
	code string,
	terminal bool,
	maxAttempts int,
	now time.Time,
) error {
	status := "pending"
	if terminal || d.Attempts >= maxAttempts {
		status = "failed"
	}
	backoff := min(time.Second*time.Duration(1<<min(d.Attempts, maxBackoffExponent)), maxRetryBackoff)
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
			Set(goqu.Record{
				"status": status, "lease_token": nil, "lease_until": nil, "last_error": code,
				"available_at": now.Add(backoff).UTC(), "updated_at": now.UTC(),
			}).
			Where(goqu.Ex{
				"event_id": d.EventID, "subscription_id": d.SubscriptionID,
				"status": "processing", "lease_token": d.Token,
			}).ToSQL()
		if err != nil {
			return err
		}
		result, err := r.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		return requireChanged(result)
	})
}

// Replay reschedules exactly one terminal failure, preserving delivered siblings.
func (r *Repository) Replay(ctx context.Context, eventID, subscriptionID string, now time.Time) error {
	query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
		Set(goqu.Record{
			"status": "pending", "attempts": 0, "last_error": nil,
			"available_at": now.UTC(), "updated_at": now.UTC(),
		}).
		Where(goqu.Ex{"event_id": eventID, "subscription_id": subscriptionID, "status": "failed"}).
		ToSQL()
	if err != nil {
		return err
	}
	result, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

// Discard explicitly waives one terminal failure and deletes completed work.
func (r *Repository) Discard(ctx context.Context, eventID, subscriptionID string, now time.Time) error {
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
			Set(goqu.Record{"status": "delivered", "updated_at": now.UTC()}).
			Where(goqu.Ex{"event_id": eventID, "subscription_id": subscriptionID, "status": "failed"}).
			ToSQL()
		if err != nil {
			return err
		}
		result, err := r.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if err = requireChanged(result); err != nil {
			return err
		}
		return r.deleteCompleted(ctx, eventID)
	})
}
