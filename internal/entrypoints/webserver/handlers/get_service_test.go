package handlers

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHandlerGetService(t *testing.T) {
	t.Parallel()

	store, err := service.NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err)
	require.NoError(t, store.ReplaceStack("payments", []service.Info{
		{
			Name:  "api",
			Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
			Spec: swarm.ServiceSpec{
				Image:             "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				Mode:              "replicated",
				Replicas:          2,
				RequestedRAMBytes: 268435456,
				RequestedCPUNano:  500000000,
				LimitRAMBytes:     536870912,
				LimitCPUNano:      1000000000,
				Labels: map[string]string{
					"com.docker.stack.namespace": "payments",
					"app.env":                    "prod",
				},
				Secrets: []swarm.ServiceSecret{
					{
						SecretName: "payments_db_password",
						Target:     "/run/secrets/payments_db_password",
					},
				},
				Network: []swarm.ServiceNetwork{
					{
						Target:  "payments_default",
						Aliases: []string{"api"},
					},
				},
			},
		},
	}))

	h := &handler{
		services: store,
	}

	resp, err := h.GetService(context.Background(), generated.GetServiceParams{
		Stack:   "payments",
		Service: "api",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "payments", resp.Stack)
	assert.Equal(t, "api", resp.Service)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", resp.Spec.Image)
	assert.Equal(t, "replicated", resp.Spec.Mode)
	assert.Equal(t, int64(2), resp.Spec.Replicas)
	assert.EqualValues(t, 268435456, resp.Spec.RequestedRAMBytes)
	assert.EqualValues(t, 500000000, resp.Spec.RequestedCPUNano)
	assert.EqualValues(t, 536870912, resp.Spec.LimitRAMBytes)
	assert.EqualValues(t, 1000000000, resp.Spec.LimitCPUNano)
	labels, ok := resp.Spec.Labels.Get()
	require.True(t, ok)
	dockerLabels, ok := labels.Docker.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"com.docker.stack.namespace": "payments",
	}, dockerLabels)
	customLabels, ok := labels.Custom.Get()
	require.True(t, ok)
	assert.Equal(t, generated.ServiceSpecLabelGroupResponse{
		"app.env": "prod",
	}, customLabels)
	require.Len(t, resp.Spec.Secrets, 1)
	assert.Equal(t, "payments_db_password", resp.Spec.Secrets[0].SecretName)
	require.Len(t, resp.Spec.Network, 1)
	assert.Equal(t, "payments_default", resp.Spec.Network[0].Target)
}

func TestHandlerGetService_NotFound(t *testing.T) {
	t.Parallel()

	store, err := service.NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err)

	h := &handler{
		services: store,
	}

	_, err = h.GetService(context.Background(), generated.GetServiceParams{
		Stack:   "payments",
		Service: "api",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, http.StatusNotFound, statusErr.code)
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

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
		history:          store,
	}

	serviceInspector.EXPECT().
		GetStatus(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return(swarm.ServiceStatus{
			Stack:   "payments",
			Service: "api",
			Spec: swarm.ServiceSpec{
				Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				Mode:  "replicated",
			},
		}, nil)

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

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
	}

	serviceInspector.EXPECT().
		GetStatus(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return(swarm.ServiceStatus{
			Stack:   "payments",
			Service: "api",
			Spec: swarm.ServiceSpec{
				Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				Mode:  "replicated",
			},
		}, nil)

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

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
		history:          store,
	}

	serviceInspector.EXPECT().
		GetStatus(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return(swarm.ServiceStatus{
			Stack:   "payments",
			Service: "api",
			Spec: swarm.ServiceSpec{
				Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				Mode:  "replicated",
			},
		}, nil)

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

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{
		serviceInspector: serviceInspector,
	}

	serviceInspector.EXPECT().
		GetStatus(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return(swarm.ServiceStatus{}, swarm.ErrServiceNotFound)

	_, err := h.ListServiceDeployments(context.Background(), generated.ListServiceDeploymentsParams{
		Stack:   "payments",
		Service: "api",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 404, statusErr.code)
}
