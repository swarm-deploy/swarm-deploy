package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"modernc.org/sqlite"
)

// ErrLeaseLost fences a worker whose claim has expired or been replaced.
var ErrLeaseLost = errors.New("outbox lease lost")

type delivery struct {
	eventID                    string
	subscriptionID             string
	token                      string
	attempts                   int
	eventType                  events.TypeName
	schemaVersion              int
	payload                    string
	occurredAt                 time.Time
	publicationSequence        int64
	traceparent                sql.NullString
	causationID                sql.NullString
	notificationDestination    sql.NullString
	notificationChannel        sql.NullString
	notificationDestinationKey sql.NullString
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
			slog.ErrorContext(ctx, "outbox worker operation failed",
				append([]any{"error_type", fmt.Sprintf("%T", err)}, safeDeliveryErrorFields(err)...)...)
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
	code, terminal := deliveryFailureCode(err)
	slog.ErrorContext(ctx, "outbox delivery failed",
		append([]any{
			"event_id", d.eventID, "subscription_id", d.subscriptionID,
			"attempt", d.attempts, "reason", code,
			"error_type", fmt.Sprintf("%T", err),
		}, safeDeliveryErrorFields(err)...)...)
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), databaseOperationTimeout)
	defer cancel()
	if retryErr := b.fail(retryCtx, d, code, terminal); retryErr != nil {
		return true, errors.Join(err, retryErr)
	}
	return true, nil
}

func deliveryFailureCode(err error) (string, bool) {
	var invalid *invalidDeliveryError
	if errors.As(err, &invalid) {
		return invalid.code, true
	}
	var sqliteErr *sqlite.Error
	var templateErr texttemplate.ExecError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded", false
	case errors.Is(err, context.Canceled):
		return "canceled", false
	case errors.Is(err, ErrLeaseLost):
		return "lease_lost", false
	case errors.As(err, &sqliteErr):
		return "database_error", false
	case errors.As(err, &templateErr):
		return "render_failed", false
	default:
		return "handler_failed", false
	}
}

// safeDeliveryErrorFields records only explicit, stable classifications. Never
// log arbitrary handler error messages: subscribers may handle credentials or
// untrusted response bodies.
func safeDeliveryErrorFields(err error) []any {
	var diagnostic interface{ SafeLogFields() []slog.Attr }
	if errors.As(err, &diagnostic) {
		fields := diagnostic.SafeLogFields()
		attrs := make([]any, 0, len(fields))
		for _, field := range fields {
			attrs = append(attrs, field)
		}
		return attrs
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return []any{"failure_source", "sqlite", "failure_kind", "database_error", "sqlite_code", sqliteErr.Code()}
	}
	var templateErr texttemplate.ExecError
	if errors.As(err, &templateErr) {
		return []any{"failure_source", "template", "failure_kind", "render_failed"}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return []any{"failure_source", "runtime", "failure_kind", "deadline_exceeded"}
	case errors.Is(err, context.Canceled):
		return []any{"failure_source", "runtime", "failure_kind", "canceled"}
	case errors.Is(err, sql.ErrNoRows):
		return []any{"failure_source", "database", "failure_kind", "not_found"}
	case errors.Is(err, ErrLeaseLost):
		return []any{"failure_source", "outbox", "failure_kind", "lease_lost"}
	default:
		return []any{"failure_source", "handler", "failure_kind", "unclassified"}
	}
}

func (b *Bus) claim(ctx context.Context) (delivery, error) {
	d := delivery{token: uuid.NewString()}
	now := b.now()
	err := b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var claimed struct {
			EventID                    string          `db:"event_id"`
			SubscriptionID             string          `db:"subscription_id"`
			Attempts                   int             `db:"attempts"`
			EventType                  events.TypeName `db:"event_type"`
			SchemaVersion              int             `db:"schema_version"`
			Payload                    string          `db:"payload"`
			OccurredAt                 time.Time       `db:"occurred_at"`
			PublicationSequence        int64           `db:"sequence"`
			Traceparent                sql.NullString  `db:"traceparent"`
			CausationID                sql.NullString  `db:"causation_id"`
			NotificationDestination    sql.NullString  `db:"notification_destination"`
			NotificationChannel        sql.NullString  `db:"notification_channel"`
			NotificationDestinationKey sql.NullString  `db:"notification_destination_key"`
		}
		query, args, buildErr := claimStatement(
			now.UTC(), now.UTC(), d.token, now.Add(b.lease).UTC(), now.UTC(),
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
		query, args, buildErr = goqu.Dialect("sqlite3").From(goqu.T("outbox_deliveries").As("d")).Prepared(true).
			Join(goqu.T("outbox_events").As("e"), goqu.On(goqu.I("e.id").Eq(goqu.I("d.event_id")))).
			Select(
				goqu.I("d.event_id"), goqu.I("d.subscription_id"), goqu.I("d.attempts"),
				goqu.I("d.notification_destination"), goqu.I("d.notification_channel"),
				goqu.I("d.notification_destination_key"), goqu.I("e.event_type"),
				goqu.I("e.schema_version"), goqu.I("e.payload"), goqu.I("e.occurred_at"),
				goqu.I("e.sequence"), goqu.I("e.traceparent"), goqu.I("e.causation_id"),
			).
			Where(goqu.Ex{"d.lease_token": d.token}).
			ToSQL()
		if buildErr != nil {
			return buildErr
		}
		if err = b.db.Get(ctx, &claimed, query, args...); err != nil {
			return err
		}
		d.eventID, d.subscriptionID, d.attempts, d.eventType =
			claimed.EventID, claimed.SubscriptionID, claimed.Attempts, claimed.EventType
		d.schemaVersion, d.payload, d.occurredAt = claimed.SchemaVersion, claimed.Payload, claimed.OccurredAt
		d.publicationSequence, d.traceparent = claimed.PublicationSequence, claimed.Traceparent
		d.causationID = claimed.CausationID
		d.notificationDestination = claimed.NotificationDestination
		d.notificationChannel = claimed.NotificationChannel
		d.notificationDestinationKey = claimed.NotificationDestinationKey
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

type invalidDeliveryError struct{ code string }

func (e *invalidDeliveryError) Error() string { return e.code }

func (b *Bus) process(ctx context.Context, d delivery) (err error) {
	spanOptions := []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			tracing.EventName.String(string(d.eventType)),
			tracing.EventSubscriberName.String(d.subscriptionID),
			tracing.EventDeliveryAttempt.Int(d.attempts),
		),
	}
	if link, ok := traceLink(d.traceparent); ok {
		spanOptions = append(spanOptions, trace.WithLinks(link))
	}
	ctx, span := b.tracer.Start(ctx, "event.Deliver", spanOptions...)
	defer func() {
		if err != nil {
			tracing.FailSpan(span, err)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}()
	event, err := codec.Decode(d.eventType, d.schemaVersion, []byte(d.payload))
	if err != nil {
		return &invalidDeliveryError{code: "unsupported_or_invalid_payload"}
	}
	b.mu.RLock()
	handler := b.registry[d.eventType][d.subscriptionID]
	b.mu.RUnlock()
	if handler == nil {
		return &invalidDeliveryError{code: "subscription_missing"}
	}
	ctx = context.WithValue(ctx, causeKey{}, d.eventID)
	ctx, cancel := context.WithTimeout(ctx, b.lease)
	defer cancel()
	envelope := events.Envelope{
		ID: d.eventID, Event: event, OccurredAt: d.occurredAt.UTC(),
		PublicationSequence: d.publicationSequence,
	}
	if notification, ok := handler.(notificationSubscriber); ok {
		if err = b.checkLease(ctx, d); err != nil {
			return err
		}
		delivered, sendErr := notification.HandleNotification(ctx, envelope)
		if sendErr != nil {
			return sendErr
		}
		return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
			return b.ack(ctx, d, delivered)
		})
	}
	if external, ok := handler.(externalSubscriber); ok {
		if err = b.checkLease(ctx, d); err != nil {
			return err
		}
		if err = invoke(ctx, external.Subscriber, envelope); err != nil {
			return err
		}
		return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
			return b.ack(ctx, d, false)
		})
	}
	return b.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if leaseErr := b.checkLease(ctx, d); leaseErr != nil {
			return leaseErr
		}
		if handlerErr := invoke(ctx, handler, envelope); handlerErr != nil {
			return handlerErr
		}
		return b.ack(ctx, d, false)
	})
}

func traceLink(traceparent sql.NullString) (trace.Link, bool) {
	if !traceparent.Valid {
		return trace.Link{}, false
	}
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{
		"traceparent": traceparent.String,
	})
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return trace.Link{}, false
	}
	return trace.Link{SpanContext: spanContext}, true
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
		}, goqu.C("lease_until").Gt(b.now().UTC())).
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

func (b *Bus) ack(ctx context.Context, d delivery, delivered bool) error {
	query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
		Set(goqu.Record{
			"status": "delivered", "lease_token": nil, "lease_until": nil,
			"last_error": nil, "updated_at": b.now().UTC(),
		}).
		Where(goqu.Ex{
			"event_id": d.eventID, "subscription_id": d.subscriptionID,
			"status": "processing", "lease_token": d.token,
		}, goqu.C("lease_until").Gt(b.now().UTC())).
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
	if delivered {
		if success := b.notificationSuccess(d); success != nil {
			if err = b.Publish(context.WithValue(ctx, causeKey{}, d.eventID), success); err != nil {
				return err
			}
		}
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
				"status": status, "lease_token": nil, "lease_until": nil, "last_error": code,
				"available_at": b.now().Add(backoff).UTC(), "updated_at": b.now().UTC(),
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
		if err = requireChanged(result); err != nil {
			return err
		}
		if status != "failed" {
			return nil
		}
		failure := b.notificationFailure(d, code)
		if failure == nil {
			return nil
		}
		return b.Publish(context.WithValue(ctx, causeKey{}, d.eventID), failure)
	})
}

func (b *Bus) notificationFailure(d delivery, code string) *events.SendNotificationFailed {
	if d.eventType == events.TypeNameSendNotificationFailed || d.eventType == events.TypeNameNotificationSent ||
		code == "subscription_missing" {
		return nil
	}
	destination, channel, key, ok := b.notificationMetadata(d)
	if !ok {
		return nil
	}
	eventType, ok := events.ParseType(string(d.eventType))
	if !ok {
		return nil
	}
	return &events.SendNotificationFailed{
		EventType: eventType, Destination: destination, Channel: channel,
		DestinationKey: key, SourceCausationID: d.causationID.String, Error: errors.New(code),
	}
}

func (b *Bus) notificationSuccess(d delivery) *events.NotificationSent {
	if d.eventType == events.TypeNameSendNotificationFailed || d.eventType == events.TypeNameNotificationSent {
		return nil
	}
	destination, channel, key, ok := b.notificationMetadata(d)
	if !ok {
		return nil
	}
	eventType, ok := events.ParseType(string(d.eventType))
	if !ok {
		return nil
	}
	return &events.NotificationSent{
		EventType: eventType, Destination: destination, Channel: channel, DestinationKey: key,
	}
}

func (b *Bus) notificationMetadata(d delivery) (string, string, string, bool) {
	destination := d.notificationDestination.String
	channel := d.notificationChannel.String
	key := d.notificationDestinationKey.String
	b.mu.RLock()
	handler := b.registry[d.eventType][d.subscriptionID]
	b.mu.RUnlock()
	if notification, ok := handler.(notificationSubscriber); ok {
		if destination == "" {
			destination = notification.destination
		}
		if channel == "" {
			channel = notification.channel
		}
		if key == "" {
			key = notification.destinationKey
		}
	}
	// Pre-migration deliveries use the registered handler metadata. If the
	// subscriber was removed, no external send happened and no signal is emitted.
	if destination == "" || key == "" {
		return "", "", "", false
	}
	if channel == "" {
		channel = fallbackNotificationChannel(destination, key)
	}
	return destination, channel, key, true
}

func fallbackNotificationChannel(destination, key string) string {
	digest := strings.TrimPrefix(key, destination+":")
	if len(digest) > notificationFallbackDigestLength {
		digest = digest[:notificationFallbackDigestLength]
	}
	return "recipient-" + digest
}

// Replay reschedules exactly one terminal failure, preserving delivered siblings.
func (b *Bus) Replay(ctx context.Context, eventID, subscriptionID string) error {
	query, args, err := goqu.Dialect("sqlite3").Update("outbox_deliveries").Prepared(true).
		Set(goqu.Record{
			"status": "pending", "attempts": 0, "last_error": nil,
			"available_at": b.now().UTC(), "updated_at": b.now().UTC(),
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
			Set(goqu.Record{"status": "delivered", "updated_at": b.now().UTC()}).
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
