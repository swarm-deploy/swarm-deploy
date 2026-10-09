package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Thiht/transactor/stdlib"
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
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
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
			return nil
		case <-ticker.C:
		}
	}
}

// ProcessNext claims and processes at most one delivery using distinct T2/T3.
// It must be called outside any publishing or subscriber transaction.
func (b *Bus) ProcessNext(ctx context.Context) (bool, error) {
	if stdlib.IsWithinTransaction(ctx) {
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
		return b.db.Get(ctx).QueryRowContext(ctx, `WITH due AS (
			SELECT event_id, subscription_id FROM outbox_deliveries
			WHERE (status='pending' AND available_at_ms<=?) OR (status='processing' AND lease_until_ms<=?)
			ORDER BY available_at_ms,event_id,subscription_id LIMIT 1
		) UPDATE outbox_deliveries SET status='processing',attempts=attempts+1,
			lease_token=?,lease_until_ms=?,updated_at_ms=?
		WHERE (event_id,subscription_id) IN (SELECT event_id,subscription_id FROM due)
		RETURNING event_id,subscription_id,attempts`, now.UnixMilli(), now.UnixMilli(), d.token,
			now.Add(b.lease).UnixMilli(), now.UnixMilli()).Scan(&d.eventID, &d.subscriptionID, &d.attempts)
	})
	return d, err
}

type invalidDeliveryError struct{ code string }

func (e *invalidDeliveryError) Error() string { return e.code }

func (b *Bus) process(ctx context.Context, d delivery) error {
	var typ events.TypeName
	var version int
	var payload string
	var occurred int64
	err := b.db.Get(ctx).QueryRowContext(ctx, `SELECT event_type,schema_version,payload,occurred_at_ms
		FROM outbox_events WHERE id=?`, d.eventID).Scan(&typ, &version, &payload, &occurred)
	if err != nil {
		return err
	}
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
	envelope := events.Envelope{ID: d.eventID, Event: event, OccurredAt: time.UnixMilli(occurred).UTC()}
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
	err := b.db.Get(ctx).QueryRowContext(ctx, `SELECT count(*) FROM outbox_deliveries
		WHERE event_id=? AND subscription_id=? AND status='processing' AND lease_token=? AND lease_until_ms>?`,
		d.eventID, d.subscriptionID, d.token, b.now().UnixMilli()).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (b *Bus) ack(ctx context.Context, d delivery) error {
	result, err := b.db.Get(ctx).ExecContext(ctx, `UPDATE outbox_deliveries
		SET status='delivered',lease_token=NULL,lease_until_ms=NULL,last_error=NULL,updated_at_ms=?
		WHERE event_id=? AND subscription_id=? AND status='processing' AND lease_token=? AND lease_until_ms>?`,
		b.now().UnixMilli(), d.eventID, d.subscriptionID, d.token, b.now().UnixMilli())
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
	_, err := b.db.Get(ctx).ExecContext(ctx, `DELETE FROM outbox_events WHERE id=? AND NOT EXISTS (
		SELECT 1 FROM outbox_deliveries WHERE event_id=outbox_events.id AND status<>'delivered')`, eventID)
	return err
}

func (b *Bus) fail(ctx context.Context, d delivery, code string, terminal bool) error {
	status := "pending"
	if terminal || d.attempts >= b.maxAttempts {
		status = "failed"
	}
	backoff := min(time.Second*time.Duration(1<<min(d.attempts, maxBackoffExponent)), maxRetryBackoff)
	return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		result, err := b.db.Get(ctx).ExecContext(ctx, `UPDATE outbox_deliveries
			SET status=?,lease_token=NULL,lease_until_ms=NULL,last_error=?,available_at_ms=?,updated_at_ms=?
			WHERE event_id=? AND subscription_id=? AND status='processing' AND lease_token=?`,
			status, code, b.now().Add(backoff).UnixMilli(), b.now().UnixMilli(), d.eventID, d.subscriptionID, d.token)
		if err != nil {
			return err
		}
		return requireChanged(result)
	})
}

// Replay reschedules exactly one terminal failure, preserving delivered siblings.
func (b *Bus) Replay(ctx context.Context, eventID, subscriptionID string) error {
	result, err := b.db.Get(ctx).ExecContext(ctx, `UPDATE outbox_deliveries
		SET status='pending',attempts=0,last_error=NULL,available_at_ms=?,updated_at_ms=?
		WHERE event_id=? AND subscription_id=? AND status='failed'`,
		b.now().UnixMilli(), b.now().UnixMilli(), eventID, subscriptionID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

// Discard explicitly waives one terminal failure and deletes completed work.
func (b *Bus) Discard(ctx context.Context, eventID, subscriptionID string) error {
	return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		result, err := b.db.Get(ctx).ExecContext(ctx, `UPDATE outbox_deliveries SET status='delivered',updated_at_ms=?
			WHERE event_id=? AND subscription_id=? AND status='failed'`, b.now().UnixMilli(), eventID, subscriptionID)
		if err != nil {
			return err
		}
		if err = requireChanged(result); err != nil {
			return err
		}
		return b.deleteCompleted(ctx, eventID)
	})
}
