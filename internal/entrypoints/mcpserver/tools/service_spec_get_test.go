package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetServiceSpecExecute(t *testing.T) {
	createdAt := time.Date(2026, time.April, 19, 10, 12, 45, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Minute)
	fakeInspector := &fakeToolServiceSpecInspector{
		service: inspector.Service{
			ID:        "service-id-1",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			Secrets: []inspector.ServiceSecret{
				{
					SecretID:   "secret-id-1",
					SecretName: "core_db_password",
					Target:     "/run/secrets/core_db_password",
				},
			},
			Spec: inspector.ServiceSpec{
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
				Secrets: []inspector.ServiceSecret{
					{
						SecretID:   "secret-id-1",
						SecretName: "core_db_password",
						Target:     "/run/secrets/core_db_password",
					},
				},
				Network: []inspector.ServiceNetwork{
					{
						Target:  "core_default",
						Aliases: []string{"api"},
					},
				},
			},
			PreviousSpec: &inspector.ServiceSpec{
				Image:    "ghcr.io/org/api:1.8.3",
				Mode:     "replicated",
				Replicas: 2,
			},
			UpdateStatus: &inspector.ServiceUpdateStatus{
				State:       "completed",
				StartedAt:   createdAt,
				CompletedAt: updatedAt,
				Message:     "update completed",
			},
		},
	}
	tool := NewGetServiceSpec(fakeInspector)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack_name":   "core",
			"service_name": "api",
		},
	})
	require.NoError(t, err, "execute service_spec_get")
	assert.Equal(t, 1, fakeInspector.called, "inspector must be called once")
	assert.Equal(t, "core", fakeInspector.stackName, "unexpected stack arg")
	assert.Equal(t, "api", fakeInspector.serviceName, "unexpected service arg")

	var payload struct {
		StackName   string            `json:"stack_name"`
		ServiceName string            `json:"service_name"`
		Service     inspector.Service `json:"service"`
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
	tool := NewGetServiceSpec(&fakeToolServiceSpecInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"service_name": "api",
		},
	})
	require.Error(t, err, "expected stack_name required error")
	assert.Contains(t, err.Error(), "stack_name is required", "unexpected error")
}

func TestGetServiceSpecExecuteRequiresServiceName(t *testing.T) {
	tool := NewGetServiceSpec(&fakeToolServiceSpecInspector{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack_name": "core",
		},
	})
	require.Error(t, err, "expected service_name required error")
	assert.Contains(t, err.Error(), "service_name is required", "unexpected error")
}

func TestGetServiceSpecExecuteReturnsInspectorError(t *testing.T) {
	tool := NewGetServiceSpec(&fakeToolServiceSpecInspector{
		err: assert.AnError,
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack_name":   "core",
			"service_name": "api",
		},
	})
	require.Error(t, err, "expected inspector error")
	assert.ErrorIs(t, err, assert.AnError, "unexpected inspector error")
}

type fakeToolServiceSpecInspector struct {
	service inspector.Service
	err     error

	called      int
	stackName   string
	serviceName string
}

func (f *fakeToolServiceSpecInspector) InspectServiceSpec(
	_ context.Context,
	stackName string,
	serviceName string,
) (inspector.Service, error) {
	f.called++
	f.stackName = stackName
	f.serviceName = serviceName
	if f.err != nil {
		return inspector.Service{}, f.err
	}

	return f.service, nil
}
