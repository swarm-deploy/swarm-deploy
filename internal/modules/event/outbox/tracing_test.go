package outbox

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func testTracerProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	return provider, recorder
}

func TestPublishPersistsTraceparent(t *testing.T) {
	provider, _ := testTracerProvider(t)
	db, bus := setup(t)
	require.NoError(t, bus.Subscribe(
		events.TypeNameNodeJoined, "history", NewMockSubscriber(gomock.NewController(t)),
	))

	ctx, publication := provider.Tracer("test").Start(t.Context(), "publication")
	require.NoError(t, bus.Publish(ctx, &events.NodeJoined{NodeID: "node-1"}))
	publication.End()

	var stored sql.NullString
	require.NoError(t, db.Get(t.Context(), &stored, "SELECT traceparent FROM outbox_events"))
	link, ok := traceLink(stored)
	require.True(t, ok)
	assert.Equal(t, publication.SpanContext().TraceID(), link.SpanContext.TraceID())
	assert.Equal(t, publication.SpanContext().SpanID(), link.SpanContext.SpanID())
	assert.Equal(t, publication.SpanContext().TraceFlags(), link.SpanContext.TraceFlags())
}

func TestPublishWithoutSpanStoresNullTraceparent(t *testing.T) {
	_, bus := setup(t)
	require.NoError(t, bus.Subscribe(
		events.TypeNameNodeJoined, "history", NewMockSubscriber(gomock.NewController(t)),
	))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node-1"}))

	var stored sql.NullString
	require.NoError(t, bus.db.Get(t.Context(), &stored, "SELECT traceparent FROM outbox_events"))
	assert.False(t, stored.Valid)
}

func TestDeliverySpansLinkPublicationAcrossSubscribersAndRetries(t *testing.T) {
	provider, recorder := testTracerProvider(t)
	db, bus := setup(t)
	ctrl := gomock.NewController(t)
	retrying := NewMockSubscriber(ctrl)
	successful := NewMockSubscriber(ctrl)
	gomock.InOrder(
		retrying.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ events.Envelope) error {
				assert.True(t, trace.SpanContextFromContext(ctx).IsValid())
				return errors.New("first attempt failed")
			},
		),
		retrying.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ events.Envelope) error {
				assert.True(t, trace.SpanContextFromContext(ctx).IsValid())
				return nil
			},
		),
	)
	successful.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ events.Envelope) error {
			assert.True(t, trace.SpanContextFromContext(ctx).IsValid())
			return nil
		},
	)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "a-retry", retrying))
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "b-success", successful))

	ctx, publication := provider.Tracer("test").Start(t.Context(), "publication")
	require.NoError(t, bus.Publish(ctx, &events.NodeJoined{NodeID: "node-1"}))
	publication.End()

	processed, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, processed)
	processed, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, processed)
	_, err = db.Exec(t.Context(),
		"UPDATE outbox_deliveries SET available_at=0 WHERE subscription_id='a-retry'",
	)
	require.NoError(t, err)
	processed, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, processed)

	deliveries := make([]sdktrace.ReadOnlySpan, 0, 3)
	for _, span := range recorder.Ended() {
		if span.Name() == "event.Deliver" {
			deliveries = append(deliveries, span)
		}
	}
	require.Len(t, deliveries, 3)
	seenSpanIDs := make(map[trace.SpanID]struct{}, len(deliveries))
	seenAttempts := map[string][]int64{}
	for _, span := range deliveries {
		assert.False(t, span.Parent().IsValid(), "delivery attempts must be root spans")
		assert.NotEqual(t, publication.SpanContext().TraceID(), span.SpanContext().TraceID())
		require.Len(t, span.Links(), 1)
		assert.Equal(t, publication.SpanContext().TraceID(), span.Links()[0].SpanContext.TraceID())
		assert.Equal(t, publication.SpanContext().SpanID(), span.Links()[0].SpanContext.SpanID())
		seenSpanIDs[span.SpanContext().SpanID()] = struct{}{}
		subscriber := spanAttribute(span, tracing.EventSubscriberName).AsString()
		seenAttempts[subscriber] = append(seenAttempts[subscriber],
			spanAttribute(span, "swarm-deploy.events.delivery.attempt").AsInt64())
		assert.Equal(t, string(events.TypeNameNodeJoined), spanAttribute(span, tracing.EventName).AsString())
	}
	assert.Len(t, seenSpanIDs, 3, "each subscriber attempt must have an independent span")
	assert.Equal(t, []int64{1, 2}, seenAttempts["a-retry"])
	assert.Equal(t, []int64{1}, seenAttempts["b-success"])

	retrySpans := spansForSubscriber(deliveries, "a-retry")
	require.Len(t, retrySpans, 2)
	assert.Equal(t, codes.Error, retrySpans[0].Status().Code)
	assert.NotEmpty(t, retrySpans[0].Events(), "subscriber errors must be recorded")
	assert.Equal(t, codes.Ok, retrySpans[1].Status().Code)
}

func TestDeliveryHandlesMissingAndInvalidTraceparent(t *testing.T) {
	tests := []struct {
		name        string
		traceparent any
	}{
		{name: "legacy NULL", traceparent: nil},
		{name: "invalid", traceparent: "not-a-traceparent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, recorder := testTracerProvider(t)
			db, bus := setup(t)
			handler := NewMockSubscriber(gomock.NewController(t))
			handler.EXPECT().Handle(gomock.Any(), gomock.Any()).Return(nil)
			require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", handler))
			payload, err := codec.Encode(&events.NodeJoined{NodeID: "legacy-node"})
			require.NoError(t, err)
			now := time.Now().UTC()
			_, err = db.Exec(t.Context(), `INSERT INTO outbox_events
				(id,event_type,schema_version,occurred_at,payload,traceparent) VALUES (?,?,?,?,?,?)`,
				"legacy-event", events.TypeNameNodeJoined, codec.Version, now, string(payload), test.traceparent)
			require.NoError(t, err)
			_, err = db.Exec(t.Context(), `INSERT INTO outbox_deliveries
				(event_id,subscription_id,available_at,updated_at) VALUES (?,?,?,?)`,
				"legacy-event", "history", now, now)
			require.NoError(t, err)

			processed, err := bus.ProcessNext(t.Context())
			require.NoError(t, err)
			require.True(t, processed)
			deliveries := recorder.Ended()
			require.Len(t, deliveries, 1)
			assert.Empty(t, deliveries[0].Links())
			assert.Equal(t, codes.Ok, deliveries[0].Status().Code)
		})
	}
}

func spanAttribute(span sdktrace.ReadOnlySpan, key attribute.Key) attribute.Value {
	for _, value := range span.Attributes() {
		if value.Key == key {
			return value.Value
		}
	}
	return attribute.Value{}
}

func spansForSubscriber(spans []sdktrace.ReadOnlySpan, subscriber string) []sdktrace.ReadOnlySpan {
	result := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if spanAttribute(span, tracing.EventSubscriberName).AsString() == subscriber {
			result = append(result, span)
		}
	}
	return result
}
