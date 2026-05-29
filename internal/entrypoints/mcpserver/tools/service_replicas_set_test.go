package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestSetServiceReplicasExecute(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().GetReplicas(gomock.Any(), serviceRef).Return(uint64(3), nil)
	manager.EXPECT().Scale(gomock.Any(), serviceRef, uint64(5)).Return(nil)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: setServiceReplicasRequest{
			Stack:    "core",
			Service:  "api",
			Replicas: uint64Pointer(5),
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
	require.Len(t, dispatcher.events, 1, "expected single dispatched event")

	replicasEvent, ok := dispatcher.events[0].(*events.ServiceReplicasIncreased)
	require.True(t, ok, "expected service replicas increased event")
	assert.Equal(t, "core", replicasEvent.StackName, "unexpected event stack")
	assert.Equal(t, "api", replicasEvent.ServiceName, "unexpected event service")
	assert.Equal(t, uint64(3), replicasEvent.PreviousReplicas, "unexpected previous replicas")
	assert.Equal(t, uint64(5), replicasEvent.CurrentReplicas, "unexpected current replicas")
}

func TestSetServiceReplicasExecuteFailsOnValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(swarm.NewMockServiceManager(ctrl), dispatcher)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: setServiceReplicasRequest{
			Stack:    "core",
			Service:  "api",
			Replicas: uint64Pointer(0),
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "replicas must be > 0", "unexpected error")
	assert.Empty(t, dispatcher.events, "validation error must not dispatch events")
}

func TestSetServiceReplicasExecuteFailsOnUpdate(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().GetReplicas(gomock.Any(), serviceRef).Return(uint64(1), nil)
	manager.EXPECT().Scale(gomock.Any(), serviceRef, uint64(2)).Return(errors.New("docker unavailable"))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: setServiceReplicasRequest{
			Stack:    "core",
			Service:  "api",
			Replicas: uint64Pointer(2),
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "update service replicas", "unexpected error")
	assert.Empty(t, dispatcher.events, "failed updates must not dispatch events")
}

func TestSetServiceReplicasExecuteDispatchesEventOnDecrease(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().GetReplicas(gomock.Any(), serviceRef).Return(uint64(5), nil)
	manager.EXPECT().Scale(gomock.Any(), serviceRef, uint64(2)).Return(nil)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: setServiceReplicasRequest{
			Stack:    "core",
			Service:  "api",
			Replicas: uint64Pointer(2),
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
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewSetServiceReplicas(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().GetReplicas(gomock.Any(), serviceRef).Return(uint64(5), nil)
	manager.EXPECT().Scale(gomock.Any(), serviceRef, uint64(5)).Return(nil)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: setServiceReplicasRequest{
			Stack:    "core",
			Service:  "api",
			Replicas: uint64Pointer(5),
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
