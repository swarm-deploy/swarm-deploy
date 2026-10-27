package outbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.opentelemetry.io/otel/trace"
)

func TestListPublications(t *testing.T) {
	db := testutil.OpenSQLite(t)
	repository := New(db)
	firstAt := time.Date(2026, time.October, 27, 10, 0, 0, 0, time.UTC)
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(t.Context(), spanContext)
	require.NoError(t, repository.Publish(ctx, &events.NodeJoined{
		NodeID: "node-1", NodeName: "first", Role: "worker",
	}, []string{"subscriber"}, firstAt))
	require.NoError(t, repository.Publish(t.Context(), &events.NodeDisconnected{
		NodeID: "node-2", NodeName: "second", Status: "down",
	}, []string{"subscriber"}, firstAt.Add(time.Second)))

	testCases := []struct {
		name   string
		filter ListFilter
		want   []events.TypeName
	}{
		{name: "all", want: []events.TypeName{events.TypeNameNodeJoined, events.TypeNameNodeDisconnected}},
		{name: "event type", filter: ListFilter{EventType: events.TypeNameNodeDisconnected},
			want: []events.TypeName{events.TypeNameNodeDisconnected}},
		{name: "empty", filter: ListFilter{EventType: events.TypeNameDeployFailed}, want: []events.TypeName{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			publications, err := repository.ListPublications(t.Context(), testCase.filter)
			require.NoError(t, err)
			types := make([]events.TypeName, 0, len(publications))
			for _, publication := range publications {
				types = append(types, publication.Type)
				assert.NotEmpty(t, publication.Envelope.ID)
				assert.NotNil(t, publication.Envelope.Event)
			}
			assert.Equal(t, testCase.want, types)
		})
	}

	publications, err := repository.ListPublications(t.Context(), ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, firstAt, publications[0].Envelope.OccurredAt)
	assert.NotEmpty(t, publications[0].Traceparent)
	assert.Less(t, publications[0].Envelope.PublicationSequence, publications[1].Envelope.PublicationSequence)
	joined, ok := publications[0].Envelope.Event.(*events.NodeJoined)
	require.True(t, ok)
	assert.Equal(t, "first", joined.NodeName)
}

func TestListRejectsInvalidPayload(t *testing.T) {
	db := testutil.OpenSQLite(t)
	repository := New(db)
	require.NoError(t, repository.Publish(t.Context(), &events.NodeJoined{
		NodeID: "node-1", NodeName: "first", Role: "worker",
	}, []string{"subscriber"}, time.Now().UTC()))
	_, err := db.Exec(t.Context(), "UPDATE outbox_events SET payload='[]'")
	require.NoError(t, err)

	_, err = repository.ListPublications(t.Context(), ListFilter{})
	require.ErrorIs(t, err, ErrInvalidPayload)
}

func TestListDeliveries(t *testing.T) {
	db := testutil.OpenSQLite(t)
	repository := New(db)
	now := time.Date(2026, time.October, 27, 10, 0, 0, 0, time.UTC)
	require.NoError(t, repository.Publish(t.Context(), &events.NodeJoined{
		NodeID: "node-1", NodeName: "first", Role: "worker",
	}, []string{"alert:telegram:first", "alert:telegram:second", "other"}, now))
	_, err := db.Exec(t.Context(), `UPDATE outbox_deliveries SET status='failed'
		WHERE subscription_id='alert:telegram:second'`)
	require.NoError(t, err)

	testCases := []struct {
		name   string
		filter ListDeliveriesFilter
		want   []string
	}{
		{name: "all", want: []string{"alert:telegram:first", "alert:telegram:second", "other"}},
		{
			name:   "subscription prefix",
			filter: ListDeliveriesFilter{SubscriptionIDPrefix: "alert:telegram:"},
			want:   []string{"alert:telegram:first", "alert:telegram:second"},
		},
		{
			name: "status",
			filter: ListDeliveriesFilter{
				Statuses: []DeliveryStatus{DeliveryStatusFailed},
			},
			want: []string{"alert:telegram:second"},
		},
		{
			name: "combined",
			filter: ListDeliveriesFilter{
				SubscriptionIDPrefix: "alert:telegram:",
				Statuses:             []DeliveryStatus{DeliveryStatusPending},
			},
			want: []string{"alert:telegram:first"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			deliveries, listErr := repository.ListDeliveries(t.Context(), testCase.filter)
			require.NoError(t, listErr)
			subscriptions := make([]string, 0, len(deliveries))
			for _, delivery := range deliveries {
				subscriptions = append(subscriptions, delivery.SubscriptionID)
				assert.NotEmpty(t, delivery.EventID)
				assert.NotEmpty(t, delivery.Status)
			}
			assert.ElementsMatch(t, testCase.want, subscriptions)
		})
	}
}
