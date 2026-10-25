package event

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	eventmetrics "github.com/swarm-deploy/swarm-deploy/internal/modules/event/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestHistoryAndMetricsKeepIndependentPolicies(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	hist, err := history.NewSQLStore(db, 100)
	require.NoError(t, err)
	group := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test"})
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(group))
	require.NoError(t, dispatcher.SubscribeMany(bus, projectionEventTypes(), "event-history", hist))
	require.NoError(t, dispatcher.SubscribeMany(bus, projectionEventTypes(), "event-metrics", eventmetrics.NewSubscriber(group.Events)))
	for _, event := range []events.Event{
		&events.NodeJoined{NodeID: "n1"}, &events.NodeDisconnected{NodeID: "n2"},
		&events.ServicePruned{StackName: "stack", ServiceName: "old"}, &events.ConfigurationChanged{},
	} {
		require.NoError(t, bus.Publish(t.Context(), event))
	}
	for {
		handled, processErr := bus.ProcessNext(t.Context())
		require.NoError(t, processErr)
		if !handled {
			break
		}
	}
	entries, err := hist.Read(t.Context())
	require.NoError(t, err)
	var historyTypes []events.TypeName
	for _, entry := range entries {
		historyTypes = append(historyTypes, entry.Type.Name())
	}
	assert.ElementsMatch(t, []events.TypeName{events.TypeNameNodeJoined, events.TypeNameServicePruned}, historyTypes)
	families, err := registry.Gather()
	require.NoError(t, err)
	counts := make(map[string]float64)
	for _, family := range families {
		if family.GetName() == "test_events_total" {
			for _, metric := range family.Metric {
				counts[metric.Label[0].GetValue()] = metric.Counter.GetValue()
			}
		}
	}
	assert.Equal(t, map[string]float64{"NodeJoined": 1, "NodeDisconnected": 1, "ServicePruned": 1}, counts)
}
