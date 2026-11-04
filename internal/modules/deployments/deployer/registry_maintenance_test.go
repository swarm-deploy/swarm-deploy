package deployer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRegistryAuthMaintenanceConvertsWithoutRunningDeploymentPipeline(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example":{"username":"synthetic","password":"synthetic-token"}}}`)
	ctrl := gomock.NewController(t)
	networks := swarm.NewMockNetworkManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	secrets := swarm.NewMockSecretManager(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	sw := &swarm.Swarm{Networks: networks, Configs: configs, Secrets: secrets, Services: manager}
	desired, err := compose.Parse([]byte(`services:
  api:
    image: registry.example/api:1
    x-init-deploy-jobs:
      - name: migrate
        image: busybox:1.36
        command: ["true"]
`))
	require.NoError(t, err)
	networks.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{
		ID: "network-id", Name: "demo_default", Scope: "swarm", Driver: "overlay",
	}, nil).Times(2)
	converted, err := converter.New(networks, configs, secrets).Convert(t.Context(), "demo", *desired)
	require.NoError(t, err)
	current := dockerswarm.Service{ID: "api-id", Spec: converted.Services[0]}
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(current, nil)
	manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ string, _ dockerswarm.Version, applied dockerswarm.ServiceSpec,
			options dockerswarm.ServiceUpdateOptions,
		) error {
			before, err := json.Marshal(current.Spec)
			require.NoError(t, err)
			after, err := json.Marshal(applied)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			require.False(t, options.QueryRegistry)
			deadline, bounded := ctx.Deadline()
			require.True(t, bounded)
			require.WithinDuration(t, time.Now().Add(serviceApplyTimeout), deadline, time.Second)
			return nil
		})
	// All other resource and service operations are strict unexpected calls,
	// including Init Job creation, resource reconciliation and convergence polling.
	native := NewDeployer(time.Millisecond, time.Second, nil, sw, nil, newTestSecretResolver())
	require.NoError(t, native.RefreshRegistryAuth(t.Context(), StackRequest{StackName: "demo", Desired: *desired}))
}

func TestRegistryAuthMaintenanceRetainsCallerCancellation(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline exceeded"}[expired], func(t *testing.T) {
			ctrl := gomock.NewController(t)
			networks := swarm.NewMockNetworkManager(ctrl)
			sw := &swarm.Swarm{Networks: networks, Configs: swarm.NewMockConfigManager(ctrl),
				Secrets: swarm.NewMockSecretManager(ctrl), Services: swarm.NewMockServiceManager(ctrl)}
			ctx, cancel := context.WithCancel(t.Context())
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			}
			cancel()
			networks.EXPECT().Get(gomock.Any(), "demo_default").DoAndReturn(
				func(actual context.Context, _ string) (swarm.Network, error) {
					if expected, exists := ctx.Deadline(); exists {
						deadline, bounded := actual.Deadline()
						require.True(t, bounded)
						require.Equal(t, expected, deadline, "maintenance must not extend an earlier caller deadline")
					}
					return swarm.Network{}, actual.Err()
				})
			native := NewDeployer(time.Millisecond, time.Second, nil, sw, nil, newTestSecretResolver())
			err := native.RefreshRegistryAuth(ctx, StackRequest{StackName: "demo", Desired: compose.Compose{
				Services: compose.Services{{Name: "api", Image: "registry.example/api:1"}},
			}})
			require.ErrorIs(t, err, ctx.Err(), "safe converter diagnostics must preserve cancellation classification")
		})
	}
}
