package notifiers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
)

func TestTraceableNotifierPassesMessageAndReceiptAndRecordsSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	inner := NewMockNotifier(gomock.NewController(t))
	inner.EXPECT().Name().Return("tg").AnyTimes()
	inner.EXPECT().Kind().Return("telegram").AnyTimes()
	msg := Message{EditMessageID: "5", Payload: Fields{"status": "resolved"}}
	inner.EXPECT().Notify(gomock.Any(), msg).Return(delivery.Receipt{MessageID: "5"}, nil)

	receipt, err := NewTraceableNotifier(inner, tp, nil).Notify(context.Background(), msg)

	require.NoError(t, err, "notify")
	assert.Equal(t, "5", receipt.MessageID, "receipt is returned through the decorator")
	require.Len(t, recorder.Ended(), 1, "span recorded for alert messages too")
	assert.Equal(t, "notifier.Notify", recorder.Ended()[0].Name(), "span name")
}
