package handlers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type fakeServiceStatusInspector struct {
	status swarm.ServiceStatus
	err    error
}

func (f fakeServiceStatusInspector) GetStatus(context.Context, swarm.ServiceReference) (swarm.ServiceStatus, error) {
	if f.err != nil {
		return swarm.ServiceStatus{}, f.err
	}

	return f.status, nil
}

func TestHandlerGetServiceStatus(t *testing.T) {
	t.Parallel()

	h := &handler{
		serviceInspector: fakeServiceStatusInspector{
			status: swarm.ServiceStatus{
				Stack:   "payments",
				Service: "api",
				Spec: swarm.ServiceSpec{
					Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					Mode:  "replicated",
				},
			},
		},
	}

	resp, err := h.GetServiceStatus(context.Background(), generated.GetServiceStatusParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "payments", resp.Stack)
	assert.Equal(t, "api", resp.Service)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", resp.Spec.Image)
	assert.Equal(t, "replicated", resp.Spec.Mode)
	assert.False(t, resp.Spec.Labels.IsSet())
}

func TestHandlerGetServiceStatus_MapsGroupedLabels(t *testing.T) {
	t.Parallel()

	h := &handler{
		serviceInspector: fakeServiceStatusInspector{
			status: swarm.ServiceStatus{
				Stack:   "payments",
				Service: "api",
				Spec: swarm.ServiceSpec{
					Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					Mode:  "replicated",
					Labels: map[string]string{
						"com.docker.stack.namespace": "payments",
						"com.docker.service.name":    "payments_api",
						"app.env":                    "prod",
					},
				},
			},
		},
	}

	resp, err := h.GetServiceStatus(context.Background(), generated.GetServiceStatusParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	labels, ok := resp.Spec.Labels.Get()
	require.True(t, ok)

	dockerLabels, ok := labels.Docker.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"com.docker.stack.namespace": "payments",
		"com.docker.service.name":    "payments_api",
	}, dockerLabels)

	customLabels, ok := labels.Custom.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"app.env": "prod",
	}, customLabels)
}

func TestHandlerListServiceDeployments_MapsFromHistory(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "history.json"), 50)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Handle(ctx, &events.DeploySuccess{
		StackName: "payments",
		Commit:    "commit-success",
	}))
	require.NoError(t, store.Handle(ctx, &events.SyncManualStarted{
		TriggeredBy: "admin",
	}))
	require.NoError(t, store.Handle(ctx, &events.DeployFailed{
		StackName: "payments",
		Commit:    "commit-failed",
		Error:     errors.New("boom"),
	}))
	require.NoError(t, store.Handle(ctx, &events.DeploySuccess{
		StackName: "infra",
		Commit:    "other-stack",
	}))

	h := &handler{
		serviceInspector: fakeServiceStatusInspector{
			status: swarm.ServiceStatus{
				Stack:   "payments",
				Service: "api",
				Spec: swarm.ServiceSpec{
					Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					Mode:  "replicated",
				},
			},
		},
		history: store,
	}

	resp, err := h.ListServiceDeployments(context.Background(), generated.ListServiceDeploymentsParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Len(t, resp.Deployments, 2)

	assert.Equal(t, generated.ServiceDeploymentStatusFailed, resp.Deployments[0].Status)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", resp.Deployments[0].Image)
	assert.Equal(t, "v1.2.3", resp.Deployments[0].ImageVersion)
	assert.True(t, resp.Deployments[0].Commit.IsSet())
	assert.Equal(t, "commit-failed", resp.Deployments[0].Commit.Value)

	assert.Equal(t, generated.ServiceDeploymentStatusSuccess, resp.Deployments[1].Status)
	assert.True(t, resp.Deployments[1].Commit.IsSet())
	assert.Equal(t, "commit-success", resp.Deployments[1].Commit.Value)
}

func TestHandlerListServiceDeployments_NoHistoryReturnsEmpty(t *testing.T) {
	t.Parallel()

	h := &handler{
		serviceInspector: fakeServiceStatusInspector{
			status: swarm.ServiceStatus{
				Stack:   "payments",
				Service: "api",
				Spec: swarm.ServiceSpec{
					Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					Mode:  "replicated",
				},
			},
		},
	}

	resp, err := h.ListServiceDeployments(context.Background(), generated.ListServiceDeploymentsParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Empty(t, resp.Deployments)
}

func TestHandlerListServiceDeployments_RespectsLimitParam(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "history.json"), 50)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, store.Handle(ctx, &events.DeploySuccess{
		StackName: "payments",
		Commit:    "commit-1",
	}))
	require.NoError(t, store.Handle(ctx, &events.DeployFailed{
		StackName: "payments",
		Commit:    "commit-2",
		Error:     errors.New("boom"),
	}))

	h := &handler{
		serviceInspector: fakeServiceStatusInspector{
			status: swarm.ServiceStatus{
				Stack:   "payments",
				Service: "api",
				Spec: swarm.ServiceSpec{
					Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					Mode:  "replicated",
				},
			},
		},
		history: store,
	}

	resp, err := h.ListServiceDeployments(context.Background(), generated.ListServiceDeploymentsParams{
		Stack:   "payments",
		Service: "api",
		Limit:   generated.NewOptInt32(1),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Deployments, 1)
	assert.Equal(t, generated.ServiceDeploymentStatusFailed, resp.Deployments[0].Status)
}

func TestHandlerListServiceDeployments_NotFound(t *testing.T) {
	t.Parallel()

	h := &handler{
		serviceInspector: fakeServiceStatusInspector{
			err: swarm.ErrServiceNotFound,
		},
	}

	_, err := h.ListServiceDeployments(context.Background(), generated.ListServiceDeploymentsParams{
		Stack:   "payments",
		Service: "api",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 404, statusErr.code)
}
