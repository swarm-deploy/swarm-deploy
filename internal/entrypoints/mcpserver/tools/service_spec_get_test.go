package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestGetServiceSpecExecute(t *testing.T) {
	createdAt := time.Date(2026, time.April, 19, 10, 12, 45, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Minute)
	ctrl := gomock.NewController(t)
	specInspector := swarm.NewMockServiceManager(ctrl)
	tool := NewGetServiceSpec(specInspector)

	expectedService := swarm.Service{
		ID:        "service-id-1",
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Secrets: []swarm.ServiceSecret{
			{
				SecretID:   "secret-id-1",
				SecretName: "core_db_password",
				Target:     "/run/secrets/core_db_password",
			},
		},
		Spec: swarm.ServiceSpec{
			Image:             "ghcr.io/org/api:1.8.4",
			Mode:              "replicated",
			Replicas:          3,
			RequestedRAMBytes: 268435456,
			RequestedCPUNano:  500000000,
			LimitRAMBytes:     536870912,
			LimitCPUNano:      1000000000,
			Labels: map[string]string{
				"com.docker.stack.namespace": "core",
			},
			Secrets: []swarm.ServiceSecret{
				{
					SecretID:   "secret-id-1",
					SecretName: "core_db_password",
					Target:     "/run/secrets/core_db_password",
				},
			},
			Network: []swarm.ServiceNetwork{
				{
					Target:  "core_default",
					Aliases: []string{"api"},
				},
			},
		},
		PreviousSpec: &swarm.ServiceSpec{
			Image:    "ghcr.io/org/api:1.8.3",
			Mode:     "replicated",
			Replicas: 2,
		},
		UpdateStatus: &swarm.ServiceUpdateStatus{
			State:       "completed",
			StartedAt:   createdAt,
			CompletedAt: updatedAt,
			Message:     "update completed",
		},
	}
	specInspector.EXPECT().
		Get(gomock.Any(), swarm.NewServiceReference("core", "api")).
		Return(expectedService, nil)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceSpecRequest{
			StackName:   "core",
			ServiceName: "api",
		},
	})
	require.NoError(t, err, "execute service_spec_get")

	var payload struct {
		StackName   string        `json:"stack_name"`
		ServiceName string        `json:"service_name"`
		Service     swarm.Service `json:"service"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "core", payload.StackName, "unexpected stack name")
	assert.Equal(t, "api", payload.ServiceName, "unexpected service name")
	assert.Equal(t, "service-id-1", payload.Service.ID, "unexpected service id")
	assert.Equal(t, "ghcr.io/org/api:1.8.4", payload.Service.Spec.Image, "unexpected service image")
	assert.Equal(t, "replicated", payload.Service.Spec.Mode, "unexpected deploy mode")
	assert.EqualValues(t, 3, payload.Service.Spec.Replicas, "unexpected replicas count")
	require.NotNil(t, payload.Service.PreviousSpec, "expected previous spec")
	assert.Equal(t, "ghcr.io/org/api:1.8.3", payload.Service.PreviousSpec.Image, "unexpected previous image")
	require.NotNil(t, payload.Service.UpdateStatus, "expected update status")
	assert.Equal(t, "completed", payload.Service.UpdateStatus.State, "unexpected update status state")
}

func TestGetServiceSpecExecuteRequiresStackName(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := NewGetServiceSpec(swarm.NewMockServiceManager(ctrl))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceSpecRequest{
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected stack_name required error")
	assert.Contains(t, err.Error(), "stack_name is required", "unexpected error")
}

func TestGetServiceSpecExecuteRequiresServiceName(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := NewGetServiceSpec(swarm.NewMockServiceManager(ctrl))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceSpecRequest{
			StackName: "core",
		},
	})
	require.Error(t, err, "expected service_name required error")
	assert.Contains(t, err.Error(), "service_name is required", "unexpected error")
}

func TestGetServiceSpecExecuteReturnsInspectorError(t *testing.T) {
	ctrl := gomock.NewController(t)
	specInspector := swarm.NewMockServiceManager(ctrl)
	tool := NewGetServiceSpec(specInspector)

	specInspector.EXPECT().
		Get(gomock.Any(), swarm.NewServiceReference("core", "api")).
		Return(swarm.Service{}, assert.AnError)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: getServiceSpecRequest{
			StackName:   "core",
			ServiceName: "api",
		},
	})
	require.Error(t, err, "expected inspector error")
	assert.ErrorIs(t, err, assert.AnError, "unexpected inspector error")
}
