package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"modernc.org/sqlite"
)

func TestWorkerPoolPollsOnceWhileIdleAndScalesForDeliveries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithSpanProcessor(recorder),
		)
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
		repository := New(testutil.OpenSQLite(t))
		subscriptions := []string{"first", "second", "third", "fourth", "fifth"}
		started := make(chan struct{}, len(subscriptions))
		release := make(chan struct{})
		worker := NewWorker(repository, func(events.TypeName, string) (DeliveryHandler, bool) {
			return DeliveryHandler{
				External: true,
				Handle: func(ctx context.Context, _ events.Envelope) error {
					started <- struct{}{}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				},
			}, true
		})
		worker.tracer = provider.Tracer("test.outbox")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()

		synctest.Wait()
		require.Len(t, recorder.Started(), 1)
		time.Sleep(workerPollInterval)
		synctest.Wait()
		require.Len(t, recorder.Started(), 2)

		require.NoError(t, repository.Publish(t.Context(), &events.NodeJoined{}, subscriptions, time.Now()))
		time.Sleep(workerPollInterval)
		synctest.Wait()
		require.Len(t, started, defaultWorkerCount)

		close(release)
		synctest.Wait()
		require.Len(t, started, len(subscriptions))
		spansAfterDrain := len(recorder.Started())
		time.Sleep(workerPollInterval)
		synctest.Wait()
		require.Len(t, recorder.Started(), spansAfterDrain+1)

		cancel()
		require.NoError(t, <-done)
	})
}

func TestProcessNextStartsDeliveryTrace(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	repository := New(testutil.OpenSQLite(t))
	worker := NewWorker(repository, func(events.TypeName, string) (DeliveryHandler, bool) {
		return DeliveryHandler{Handle: func(context.Context, events.Envelope) error { return nil }}, true
	})
	worker.tracer = provider.Tracer("test.outbox")

	ctx, publisher := worker.tracer.Start(t.Context(), "publish")
	require.NoError(t, repository.Publish(ctx, &events.NodeJoined{
		NodeID: "node-1", NodeName: "test-node", Role: "worker",
	}, []string{"subscriber"}, time.Now()))
	publisher.End()

	handled, err := worker.ProcessNext(ctx)
	require.NoError(t, err)
	require.True(t, handled)
	spans := recorder.Ended()
	require.Len(t, spans, 3)
	delivery, processNext := spans[1], spans[2]
	assert.Equal(t, "event.ProcessNext", processNext.Name())
	assert.False(t, processNext.Parent().IsValid())
	assert.Equal(t, "event.Deliver", delivery.Name())
	assert.Equal(t, trace.SpanKindConsumer, delivery.SpanKind())
	assert.True(t, processNext.SpanContext().Equal(delivery.Parent()))
	require.Len(t, delivery.Links(), 1)
	assert.Equal(t, publisher.SpanContext().TraceID(), delivery.Links()[0].SpanContext.TraceID())
	assert.Equal(t, publisher.SpanContext().SpanID(), delivery.Links()[0].SpanContext.SpanID())
}

type safeTestError struct{}

func (safeTestError) Error() string { return "token=private-value" }
func (safeTestError) SafeLogFields() []slog.Attr {
	return []slog.Attr{slog.String("failure_source", "telegram"), slog.Int("http_status", 429)}
}

func TestSafeDeliveryErrorFields(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantSource string
		wantKind   string
	}{
		{"unknown secret", errors.New("token=private-value"), "handler", "unclassified"},
		{"deadline", context.DeadlineExceeded, "runtime", "deadline_exceeded"},
		{"sqlite", &sqlite.Error{}, "sqlite", "database_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := safeDeliveryErrorFields(tt.err)
			got := map[string]any{}
			for i := 0; i+1 < len(fields); i += 2 {
				got[fields[i].(string)] = fields[i+1]
			}
			assert.Equal(t, tt.wantSource, got["failure_source"])
			assert.Equal(t, tt.wantKind, got["failure_kind"])
			assert.NotContains(t, got, "error")
		})
	}
	fields := safeDeliveryErrorFields(errors.Join(errors.New("token=private-value"), safeTestError{}))
	require.Len(t, fields, 2)
	assert.Equal(t, "failure_source=telegram", fields[0].(slog.Attr).String())
	assert.Equal(t, "http_status=429", fields[1].(slog.Attr).String())
}
