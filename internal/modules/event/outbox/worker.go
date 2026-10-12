package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// ErrLeaseLost fences a worker whose claim has expired or been replaced.
var ErrLeaseLost = errors.New("outbox lease lost")

type delivery struct {
	eventID        string
	subscriptionID string
	token          string
	attempts       int
}

// Run processes committed work until cancellation. Unfinished leases survive
// shutdown and can be reclaimed after expiry by the next process.
func (b *Bus) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	for range b.workers {
		workers.Go(func() { b.runWorker(ctx) })
	}
	workers.Wait()
	return nil
}

func (b *Bus) runWorker(ctx context.Context) {
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		handled, err := b.ProcessNext(ctx)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "outbox worker operation failed", "error_type", fmt.Sprintf("%T", err))
		}
		if handled && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessNext claims and processes at most one delivery using distinct T2/T3.
// It must be called outside any publishing or subscriber transaction.
func (b *Bus) ProcessNext(ctx context.Context) (bool, error) {
	if b.db.IsWithinTransaction(ctx) {
		return false, errors.New("worker cannot run in a publisher transaction")
	}
	b.mu.Lock()
	b.started = true
	b.mu.Unlock()
	d, err := b.claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = b.process(ctx, d)
	if err == nil {
		return true, nil
	}
	// Do not persist arbitrary handler errors: they may contain HTTP credentials,
	// command output or secret values. Stable codes and delivery IDs enable diagnosis.
	code := "handler_failed"
	var invalid *invalidDeliveryError
	terminal := errors.As(err, &invalid)
	if terminal {
		code = invalid.code
	}
	slog.ErrorContext(ctx, "outbox delivery failed", "event_id", d.eventID,
		"subscription_id", d.subscriptionID, "attempt", d.attempts, "reason", code,
		"error_type", fmt.Sprintf("%T", err))
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), databaseOperationTimeout)
	defer cancel()
	if retryErr := b.fail(retryCtx, d, code, terminal); retryErr != nil {
		return true, errors.Join(err, retryErr)
	}
	return true, nil
}

func (b *Bus) claim(ctx context.Context) (delivery, error) {
	d := delivery{token: uuid.NewString()}
	now := b.now()
	err := b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var claimed struct {
			EventID        string `db:"event_id"`
			SubscriptionID string `db:"subscription_id"`
			Attempts       int    `db:"attempts"`
		}
		query, args, buildErr := claimStatement(
			now.UnixMilli(), now.UnixMilli(), d.token, now.Add(b.lease).UnixMilli(), now.UnixMilli(),
		)
		if buildErr != nil {
			return buildErr
		}
		result, err := b.db.Exec(ctx, query, args...)
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
			Where(goqu.Ex{"lease_token": d.token}).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		if err = b.db.Get(ctx, &claimed, query, args...); err != nil {
			return err
		}
		d.eventID, d.subscriptionID, d.attempts = claimed.EventID, claimed.SubscriptionID, claimed.Attempts
		return nil
	})
	return d, err
}

func claimStatement(nowMs, expiredMs int64, token string, leaseUntilMs, updatedAtMs int64) (string, []any, error) {
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
				goqu.And(goqu.I("candidate.status").Eq("pending"), goqu.I("candidate.available_at_ms").Lte(nowMs)),
				goqu.And(goqu.I("candidate.status").Eq("processing"), goqu.I("candidate.lease_until_ms").Lte(expiredMs)),
			),
			goqu.L("NOT EXISTS ?", earlierPending),
		).
		Order(
			goqu.I("candidate.available_at_ms").Asc(),
			goqu.I("candidate_event.sequence").Asc(),
			goqu.I("candidate.subscription_id").Asc(),
		).
		Limit(1)
	return dialect.Update("outbox_deliveries").Prepared(true).
		With("due", due).
		Set(goqu.Record{
			"status":         "processing",
			"attempts":       goqu.L("attempts+1"),
			"lease_token":    token,
			"lease_until_ms": leaseUntilMs,
			"updated_at_ms":  updatedAtMs,
		}).
		Where(goqu.L("(event_id,subscription_id) IN ?", dialect.From("due").Select("event_id", "subscription_id"))).
		ToSQL()
}

type invalidDeliveryError struct{ code string }

func (e *invalidDeliveryError) Error() string { return e.code }

func (b *Bus) process(ctx context.Context, d delivery) error {
	var row struct {
		EventType           events.TypeName `db:"event_type"`
		SchemaVersion       int             `db:"schema_version"`
		Payload             string          `db:"payload"`
		OccurredAtMs        int64           `db:"occurred_at_ms"`
		PublicationSequence int64           `db:"sequence"`
	}
	query, args, err := goqu.Dialect("sqlite3").From("outbox_events").Prepared(true).
		Select("event_type", "schema_version", "payload", "occurred_at_ms", "sequence").
		Where(goqu.Ex{"id": d.eventID}).ToSQL()
	if err != nil {
		return err
	}
	err = b.db.Get(ctx, &row, query, args...)
	if err != nil {
		return err
	}
	typ, version, payload, occurred, publicationSequence := row.EventType, row.SchemaVersion, row.Payload,
		row.OccurredAtMs, row.PublicationSequence
	event, err := codec.Decode(typ, version, []byte(payload))
	if err != nil {
		return &invalidDeliveryError{code: "unsupported_or_invalid_payload"}
	}
	b.mu.RLock()
	handler := b.registry[typ][d.subscriptionID]
	b.mu.RUnlock()
	if handler == nil {
		return &invalidDeliveryError{code: "subscription_missing"}
	}
	ctx = context.WithValue(ctx, causeKey{}, d.eventID)
	ctx, cancel := context.WithTimeout(ctx, b.lease)
	defer cancel()
	envelope := events.Envelope{
		ID: d.eventID, Event: event, OccurredAt: time.UnixMilli(occurred).UTC(),
		PublicationSequence: publicationSequence,
	}
	if external, ok := handler.(externalSubscriber); ok {
		if err = b.checkLease(ctx, d); err != nil {
			return err
		}
		if err = invoke(ctx, external.Subscriber, envelope); err != nil {
			return err
		}
		return b.db.WithinTransaction(ctx, func(ctx context.Context) error { return b.ack(ctx, d) })
	}
	return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if leaseErr := b.checkLease(ctx, d); leaseErr != nil {
			return leaseErr
		}
		if handlerErr := invoke(ctx, handler, envelope); handlerErr != nil {
			return handlerErr
		}
		return b.ack(ctx, d)
	})
}

func invoke(ctx context.Context, handler Subscriber, envelope events.Envelope) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("outbox handler panicked")
		}
	}()
	return handler.Handle(ctx, envelope)
}

func (b *Bus) checkLease(ctx context.Context, d delivery) error {
	var count int
	query, args, err := goqu.Dialect("sqlite3").From("outbox_deliveries").Prepared(true).
		Select(goqu.COUNT("*")).
		Where(goqu.Ex{
			"event_id": d.eventID, "subscription_id": d.subscriptionID,
			"status": "processing", "lease_token": d.token,
		}, goqu.C("lease_until_ms").Gt(b.now().UnixMilli())).
		ToSQL()
	if err != nil {
		return err
	}
	err = b.db.Get(ctx, &count, query, args...)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (b *Bus) ack(ctx context.Context, d delivery) error {
	query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
		Set(goqu.Record{
			"status": "delivered", "lease_token": nil, "lease_until_ms": nil,
			"last_error": nil, "updated_at_ms": b.now().UnixMilli(),
		}).
		Where(goqu.Ex{
			"event_id": d.eventID, "subscription_id": d.subscriptionID,
			"status": "processing", "lease_token": d.token,
		}, goqu.C("lease_until_ms").Gt(b.now().UnixMilli())).
		ToSQL()
	if err != nil {
		return err
	}
	result, err := b.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if err = requireChanged(result); err != nil {
		return err
	}
	return b.deleteCompleted(ctx, d.eventID)
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

func (b *Bus) deleteCompleted(ctx context.Context, eventID string) error {
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
	_, err = b.db.Exec(ctx, query, args...)
	return err
}

func (b *Bus) fail(ctx context.Context, d delivery, code string, terminal bool) error {
	status := "pending"
	if terminal || d.attempts >= b.maxAttempts {
		status = "failed"
	}
	backoff := min(time.Second*time.Duration(1<<min(d.attempts, maxBackoffExponent)), maxRetryBackoff)
	return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
			Set(goqu.Record{
				"status": status, "lease_token": nil, "lease_until_ms": nil, "last_error": code,
				"available_at_ms": b.now().Add(backoff).UnixMilli(), "updated_at_ms": b.now().UnixMilli(),
			}).
			Where(goqu.Ex{
				"event_id": d.eventID, "subscription_id": d.subscriptionID,
				"status": "processing", "lease_token": d.token,
			}).ToSQL()
		if err != nil {
			return err
		}
		result, err := b.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		return requireChanged(result)
	})
}

// Replay reschedules exactly one terminal failure, preserving delivered siblings.
func (b *Bus) Replay(ctx context.Context, eventID, subscriptionID string) error {
	query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
		Set(goqu.Record{
			"status": "pending", "attempts": 0, "last_error": nil,
			"available_at_ms": b.now().UnixMilli(), "updated_at_ms": b.now().UnixMilli(),
		}).
		Where(goqu.Ex{"event_id": eventID, "subscription_id": subscriptionID, "status": "failed"}).
		ToSQL()
	if err != nil {
		return err
	}
	result, err := b.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

// Discard explicitly waives one terminal failure and deletes completed work.
func (b *Bus) Discard(ctx context.Context, eventID, subscriptionID string) error {
	return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
			Set(goqu.Record{"status": "delivered", "updated_at_ms": b.now().UnixMilli()}).
			Where(goqu.Ex{"event_id": eventID, "subscription_id": subscriptionID, "status": "failed"}).
			ToSQL()
		if err != nil {
			return err
		}
		result, err := b.db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if err = requireChanged(result); err != nil {
			return err
		}
		return b.deleteCompleted(ctx, eventID)
	})
}
