package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	texttemplate "text/template"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"modernc.org/sqlite"
)

const (
	workerPollInterval = 250 * time.Millisecond
	defaultLease       = 5 * time.Minute
	defaultMaxAttempts = 12
	defaultWorkerCount = 4
)

// DeliveryHandler invokes one registered destination.
type DeliveryHandler struct {
	// Handle processes the decoded event envelope.
	Handle func(context.Context, events.Envelope) error
	// External reports whether the handler performs I/O outside the database transaction.
	External bool
}

// HandlerResolver returns the registered handler for a durable destination.
type HandlerResolver func(events.TypeName, string) (DeliveryHandler, bool)

// Worker claims and processes durable Outbox deliveries.
type Worker struct {
	repository  *Repository
	resolve     HandlerResolver
	now         func() time.Time
	lease       time.Duration
	maxAttempts int
	workers     int
	tracer      trace.Tracer
}

// WorkerOption configures a delivery worker.
type WorkerOption func(*Worker)

// WithClock overrides the worker clock.
func WithClock(now func() time.Time) WorkerOption {
	return func(worker *Worker) { worker.now = now }
}

// WithMaxAttempts overrides the number of delivery attempts before parking.
func WithMaxAttempts(maxAttempts int) WorkerOption {
	return func(worker *Worker) { worker.maxAttempts = maxAttempts }
}

// NewWorker constructs an Outbox delivery worker.
func NewWorker(repository *Repository, resolve HandlerResolver, options ...WorkerOption) *Worker {
	worker := &Worker{
		repository:  repository,
		resolve:     resolve,
		now:         time.Now,
		lease:       defaultLease,
		maxAttempts: defaultMaxAttempts,
		workers:     defaultWorkerCount,
		tracer:      otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"),
	}
	for _, option := range options {
		option(worker)
	}
	return worker
}

// Run processes committed work until cancellation. Unfinished leases survive
// shutdown and can be reclaimed after expiry by the next process.
func (w *Worker) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	for range w.workers {
		workers.Go(func() { w.run(ctx) })
	}
	workers.Wait()
	return nil
}

func (w *Worker) run(ctx context.Context) {
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		handled, err := w.ProcessNext(ctx)
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
func (w *Worker) ProcessNext(ctx context.Context) (bool, error) {
	d, err := w.Claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = w.Process(ctx, d)
	if err == nil {
		return true, nil
	}
	code := "handler_failed"
	var invalid *invalidDeliveryError
	terminal := errors.As(err, &invalid)
	if terminal {
		code = invalid.code
	}
	slog.ErrorContext(ctx, "outbox delivery failed",
		append([]any{
			"event_id", d.EventID, "subscription_id", d.SubscriptionID,
			"attempt", d.Attempts, "reason", code,
			"error_type", fmt.Sprintf("%T", err),
		}, safeDeliveryErrorFields(err)...)...)
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), databaseOperationTimeout)
	defer cancel()
	if retryErr := w.repository.Fail(retryCtx, d, code, terminal, w.maxAttempts, w.now()); retryErr != nil {
		return true, errors.Join(err, retryErr)
	}
	return true, nil
}

// Claim atomically leases at most one due delivery.
func (w *Worker) Claim(ctx context.Context) (Delivery, error) {
	return w.repository.Claim(ctx, w.now(), w.lease)
}

type invalidDeliveryError struct{ code string }

func (e *invalidDeliveryError) Error() string { return e.code }

// Process invokes the subscriber for a claimed delivery and acknowledges it.
func (w *Worker) Process(ctx context.Context, d Delivery) (err error) {
	publication, loadErr := w.repository.Load(ctx, d)
	if loadErr != nil && !errors.Is(loadErr, ErrInvalidPayload) {
		return loadErr
	}
	spanOptions := []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			tracing.EventName.String(string(publication.Type)),
			tracing.EventSubscriberName.String(d.SubscriptionID),
			tracing.EventDeliveryAttempt.Int(d.Attempts),
		),
	}
	if link, ok := traceLink(publication.Traceparent); ok {
		spanOptions = append(spanOptions, trace.WithLinks(link))
	}
	ctx, span := w.tracer.Start(ctx, "event.Deliver", spanOptions...)
	defer func() {
		if err != nil {
			tracing.FailSpan(span, err)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}()
	if loadErr != nil {
		return &invalidDeliveryError{code: "unsupported_or_invalid_payload"}
	}
	handler, ok := w.resolve(publication.Type, d.SubscriptionID)
	if !ok {
		return &invalidDeliveryError{code: "subscription_missing"}
	}
	ctx = WithCause(ctx, d.EventID)
	ctx, cancel := context.WithTimeout(ctx, w.lease)
	defer cancel()
	if handler.External {
		if err = w.repository.CheckLease(ctx, d, w.now()); err != nil {
			return err
		}
		if err = invoke(ctx, handler.Handle, publication.Envelope); err != nil {
			return err
		}
		return w.repository.WithinTransaction(ctx, func(ctx context.Context) error {
			return w.Acknowledge(ctx, d)
		})
	}
	return w.repository.WithinTransaction(ctx, func(ctx context.Context) error {
		if leaseErr := w.repository.CheckLease(ctx, d, w.now()); leaseErr != nil {
			return leaseErr
		}
		if handlerErr := invoke(ctx, handler.Handle, publication.Envelope); handlerErr != nil {
			return handlerErr
		}
		return w.Acknowledge(ctx, d)
	})
}

// Acknowledge marks a claimed delivery complete.
func (w *Worker) Acknowledge(ctx context.Context, d Delivery) error {
	return w.repository.Acknowledge(ctx, d, w.now())
}

func traceLink(traceparent string) (trace.Link, bool) {
	if traceparent == "" {
		return trace.Link{}, false
	}
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{
		"traceparent": traceparent,
	})
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return trace.Link{}, false
	}
	return trace.Link{SpanContext: spanContext}, true
}

func invoke(
	ctx context.Context,
	handler func(context.Context, events.Envelope) error,
	envelope events.Envelope,
) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("outbox handler panicked")
		}
	}()
	return handler(ctx, envelope)
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
