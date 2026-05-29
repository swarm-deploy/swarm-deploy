package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRestartServiceExecute(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewRestartService(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().Restart(gomock.Any(), serviceRef).Return(uint64(3), nil)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: restartServiceRequest{
			Stack:   "core",
			Service: "api",
		},
	})
	require.NoError(t, err, "execute service_restart_trigger")

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
	assert.Equal(t, uint64(3), payload.Replicas, "unexpected replicas")

	require.Len(t, dispatcher.events, 1, "expected single dispatched event")
	restartEvent, ok := dispatcher.events[0].(*events.ServiceRestarted)
	require.True(t, ok, "expected service restarted event")
	assert.Equal(t, "core", restartEvent.StackName, "unexpected event stack")
	assert.Equal(t, "api", restartEvent.ServiceName, "unexpected event service")
}

func TestRestartServiceExecuteFailsOnInspect(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewRestartService(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().
		Restart(gomock.Any(), serviceRef).
		Return(uint64(0), errors.New("inspect service replicas: inspect unavailable"))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: restartServiceRequest{
			Stack:   "core",
			Service: "api",
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "inspect service replicas", "unexpected error")
	assert.Empty(t, dispatcher.events, "failed inspect must not dispatch events")
}

func TestRestartServiceExecuteFailsOnRestore(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	dispatcher := &fakeEventDispatcher{}
	tool := NewRestartService(manager, dispatcher)

	serviceRef := swarm.NewServiceReference("core", "api")
	manager.EXPECT().
		Restart(gomock.Any(), serviceRef).
		Return(uint64(0), errors.New("restore service replicas to 2: restore failed"))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: restartServiceRequest{
			Stack:   "core",
			Service: "api",
		},
	})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "restore service replicas", "unexpected error")
	assert.Empty(t, dispatcher.events, "failed restore must not dispatch events")
}
