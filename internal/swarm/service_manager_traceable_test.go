package swarm

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
)

func TestTraceableServiceManagerRuntimeOperations(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	ctrl := gomock.NewController(t)
	manager := NewMockServiceManager(ctrl)
	traceable := traceServiceManager(provider.Tracer("test"), manager)

	manager.EXPECT().
		ListServices(gomock.Any(), ListServicesFilter{Stack: "infra"}).
		Return([]StackService{{ID: "service-1"}}, nil)
	_, err := traceable.ListServices(t.Context(), ListServicesFilter{Stack: "infra"})
	require.NoError(t, err)

	manager.EXPECT().ListTasks(gomock.Any(), "service-1").Return([]ServiceTask{{ID: "task-1"}}, nil)
	_, err = traceable.ListTasks(t.Context(), "service-1")
	require.NoError(t, err)

	manager.EXPECT().GetTask(gomock.Any(), "task-1").Return(ServiceTaskDetails{
		ID:          "task-1",
		ServiceID:   "service-1",
		Environment: []string{"DB_PASSWORD=synthetic-private-value"},
	}, nil)
	_, err = traceable.GetTask(t.Context(), "task-1")
	require.NoError(t, err)

	spans := recorder.Ended()
	require.Len(t, spans, 3)
	assert.Equal(t, "swarm.ListServices", spans[0].Name())
	assert.Equal(t, "swarm.ListTasks", spans[1].Name())
	assert.Equal(t, "swarm.GetTask", spans[2].Name())
	assert.Contains(t, fmt.Sprint(spans[0].Attributes()), "infra")
	assert.Contains(t, fmt.Sprint(spans[0].Attributes()), "services.count")
	assert.Contains(t, fmt.Sprint(spans[1].Attributes()), "service-1")
	assert.Contains(t, fmt.Sprint(spans[2].Attributes()), "task-1")
	for _, span := range spans {
		diagnostic := fmt.Sprint(span.Attributes(), span.Events(), span.Status())
		assert.NotContains(t, diagnostic, "DB_PASSWORD")
		assert.NotContains(t, diagnostic, "synthetic-private-value")
	}
}
