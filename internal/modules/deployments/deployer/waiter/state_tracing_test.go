package waiter

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWaitStateTracingAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { require.NoError(t, provider.Shutdown(t.Context())) }()

	_, span := provider.Tracer("test").Start(t.Context(), "service wait")
	state := waitState{last: make(map[string]ServiceProgress), observed: 1, converged: 0}
	state.emit(span, nil, ServiceProgress{
		ServiceID: "service-id", Name: "service", State: ServicePending,
		Desired: 3, Running: 2, Message: "waiting",
	})
	finishServiceWaitSpan(span, time.Now(), state, errors.New("not converged"))
	span.End()

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Events(), 1)

	eventAttrs := make(map[attribute.Key]attribute.Value)
	for _, item := range spans[0].Events()[0].Attributes {
		eventAttrs[item.Key] = item.Value
	}
	require.Equal(t, "pending", eventAttrs[tracing.DeploymentServiceState].AsString())
	require.Equal(t, attribute.INT64, eventAttrs[tracing.DeploymentServiceDesiredTasks].Type())
	require.Equal(t, int64(3), eventAttrs[tracing.DeploymentServiceDesiredTasks].AsInt64())
	require.Equal(t, attribute.INT64, eventAttrs[tracing.DeploymentServiceRunningTasks].Type())
	require.Equal(t, int64(2), eventAttrs[tracing.DeploymentServiceRunningTasks].AsInt64())

	spanAttrs := make(map[attribute.Key]attribute.Value)
	for _, item := range spans[0].Attributes() {
		spanAttrs[item.Key] = item.Value
	}
	require.Equal(t, "failed", spanAttrs[tracing.DeploymentServiceWaitOutcome].AsString())
	require.Equal(t, attribute.INT64, spanAttrs[tracing.DeploymentServiceWaitDurationMS].Type())
	require.Equal(t, int64(1), spanAttrs[tracing.DeploymentServiceWaitObservedServices].AsInt64())
	require.Equal(t, int64(0), spanAttrs[tracing.DeploymentServiceWaitConvergedServices].AsInt64())
	require.Equal(t, "not converged", spanAttrs[tracing.DeploymentServiceWaitFailureReason].AsString())
}
