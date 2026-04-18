package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetServiceReplicasExecute(t *testing.T) {
	manager := &fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 3,
		},
	}
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": "5",
		},
	})
	require.NoError(t, err, "execute service_replicas_set")

	var payload struct {
		Stack    string `json:"stack"`
		Service  string `json:"service"`
		Replicas uint64 `json:"replicas"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "core", payload.Stack, "unexpected stack")
	assert.Equal(t, "api", payload.Service, "unexpected service")
	assert.Equal(t, uint64(5), payload.Replicas, "unexpected replicas")
	assert.Equal(t, 1, manager.inspectCalled, "expected single inspect call")
	assert.Equal(t, "core", manager.inspectedStack, "unexpected inspected stack")
	assert.Equal(t, "api", manager.inspectedService, "unexpected inspected service")
	assert.Equal(t, 1, manager.updateCalled, "expected single update call")
	assert.Equal(t, "core", manager.updatedStack, "unexpected updated stack")
	assert.Equal(t, "api", manager.updatedService, "unexpected updated service")
	assert.Equal(t, uint64(5), manager.updatedReplicas, "unexpected updated replicas")
	require.Len(t, dispatcher.events, 1, "expected single dispatched event")

	replicasEvent, ok := dispatcher.events[0].(*events.ServiceReplicasIncreased)
	require.True(t, ok, "expected service replicas increased event")
	assert.Equal(t, "core", replicasEvent.StackName, "unexpected event stack")
	assert.Equal(t, "api", replicasEvent.ServiceName, "unexpected event service")
	assert.Equal(t, uint64(3), replicasEvent.PreviousReplicas, "unexpected previous replicas")
	assert.Equal(t, uint64(5), replicasEvent.CurrentReplicas, "unexpected current replicas")
}

func TestSetServiceReplicasExecuteFailsOnValidation(t *testing.T) {
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(&fakeServiceReplicasManager{}, dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": 0,
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "replicas must be > 0", "unexpected error")
	assert.Empty(t, dispatcher.events, "validation error must not dispatch events")
}

func TestSetServiceReplicasExecuteFailsOnUpdate(t *testing.T) {
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(&fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 1,
		},
		updateErr: errors.New("docker unavailable"),
	}, dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": 2,
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "update service replicas", "unexpected error")
	assert.Empty(t, dispatcher.events, "failed updates must not dispatch events")
}

func TestSetServiceReplicasExecuteDispatchesEventOnDecrease(t *testing.T) {
	manager := &fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 5,
		},
	}
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": 2,
		},
	})
	require.NoError(t, err, "execute service_replicas_set")
	require.Len(t, dispatcher.events, 1, "expected single dispatched event")

	replicasEvent, ok := dispatcher.events[0].(*events.ServiceReplicasDecreased)
	require.True(t, ok, "expected service replicas decreased event")
	assert.Equal(t, "core", replicasEvent.StackName, "unexpected event stack")
	assert.Equal(t, "api", replicasEvent.ServiceName, "unexpected event service")
	assert.Equal(t, uint64(5), replicasEvent.PreviousReplicas, "unexpected previous replicas")
	assert.Equal(t, uint64(2), replicasEvent.CurrentReplicas, "unexpected current replicas")
}

func TestSetServiceReplicasExecuteSkipsEventOnSameReplicas(t *testing.T) {
	manager := &fakeServiceReplicasManager{
		replicasByService: map[string]uint64{
			"core_api": 5,
		},
	}
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":    "core",
			"service":  "api",
			"replicas": 5,
		},
	})
	require.NoError(t, err, "execute service_replicas_set")
	assert.Empty(t, dispatcher.events, "same replicas count must not dispatch events")
}

type fakeEventDispatcher struct {
	events []events.Event
}

func (f *fakeEventDispatcher) Subscribe(_ events.Type, _ dispatcher.Subscriber) {}

func (f *fakeEventDispatcher) Dispatch(_ context.Context, event events.Event) {
	f.events = append(f.events, event)
}

func (f *fakeEventDispatcher) Shutdown(_ context.Context) error {
	return nil
}
