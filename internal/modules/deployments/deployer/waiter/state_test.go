package waiter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func TestFinishServiceWaitSpanMasksError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	_, span := provider.Tracer("test").Start(t.Context(), "wait for services")

	finishServiceWaitSpan(span, time.Now(), waitState{}, errors.New(`registry request failed: {"token":"private-value"}`))
	span.End()

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	ended := spans[0]
	const masked = `registry request failed: {"token":"****"}`
	assert.Equal(t, sdktrace.Status{Code: codes.Error, Description: masked}, ended.Status())
	assert.Contains(t, ended.Attributes(), tracing.DeploymentServiceWaitFailureReason.String(masked))
	events := ended.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "exception", events[0].Name)
	assert.Contains(t, events[0].Attributes, semconv.ExceptionMessageKey.String(masked))
	for _, attr := range events[0].Attributes {
		assert.NotContains(t, attr.Value.Emit(), "private-value")
	}
}
