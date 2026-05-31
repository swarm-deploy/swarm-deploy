package livemanifest

import (
	"context"
	"errors"
	"testing"
	"time"

	container "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestComputerComputeStackMapsRawServiceSpec(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networkManager := swarm.NewMockNetworkManager(ctrl)

	replicas := uint64(3)
	maxAttempts := uint64(5)
	maxReplicas := uint64(2)
	restartDelay := 3 * time.Second
	restartWindow := 30 * time.Second

	stack := Stack{
		Name: "payments",
		Services: []swarm.StackService{
			{
				Name: "api",
				ServiceSpec: dockerswarm.ServiceSpec{
					Annotations: dockerswarm.Annotations{
						Labels: map[string]string{
							"org.swarm-deploy.service.managed": "true",
						},
					},
					TaskTemplate: dockerswarm.TaskSpec{
						ContainerSpec: &dockerswarm.ContainerSpec{
							Image:   "ghcr.io/swarm-deploy/payments-api:v1.2.3",
							Command: []string{"/app/api"},
							Args:    []string{"--port", "8080"},
							Env:     []string{"LOG_LEVEL=debug"},
							Labels: map[string]string{
								"app.env": "prod",
							},
							Secrets: []*dockerswarm.SecretReference{
								{
									SecretName: "db_password",
									File: &dockerswarm.SecretReferenceFileTarget{
										Name: "/run/secrets/db_password",
									},
								},
							},
							Configs: []*dockerswarm.ConfigReference{
								{
									ConfigName: "api_config",
									File: &dockerswarm.ConfigReferenceFileTarget{
										Name: "/etc/app/config.yml",
									},
								},
							},
							Healthcheck: &container.HealthConfig{
								Test:          []string{"CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"},
								Interval:      10 * time.Second,
								Timeout:       3 * time.Second,
								StartPeriod:   5 * time.Second,
								StartInterval: 2 * time.Second,
								Retries:       4,
							},
							Mounts: []mount.Mount{
								{
									Type:     mount.TypeBind,
									Source:   "/srv/payments/data",
									Target:   "/var/lib/payments",
									ReadOnly: true,
									BindOptions: &mount.BindOptions{
										Propagation:      mount.PropagationRSlave,
										CreateMountpoint: true,
									},
								},
								{
									Type:   mount.TypeVolume,
									Source: "payments-cache",
									Target: "/var/cache/payments",
									VolumeOptions: &mount.VolumeOptions{
										NoCopy:  true,
										Subpath: "api",
									},
								},
							},
						},
						Resources: &dockerswarm.ResourceRequirements{
							Limits: &dockerswarm.Limit{
								NanoCPUs:    500000000,
								MemoryBytes: 268435456,
								Pids:        128,
							},
							Reservations: &dockerswarm.Resources{
								NanoCPUs:    250000000,
								MemoryBytes: 134217728,
							},
						},
						RestartPolicy: &dockerswarm.RestartPolicy{
							Condition:   dockerswarm.RestartPolicyConditionOnFailure,
							Delay:       &restartDelay,
							MaxAttempts: &maxAttempts,
							Window:      &restartWindow,
						},
						Placement: &dockerswarm.Placement{
							Constraints: []string{"node.role == manager"},
							Preferences: []dockerswarm.PlacementPreference{
								{
									Spread: &dockerswarm.SpreadOver{SpreadDescriptor: "node.labels.zone"},
								},
							},
							MaxReplicas: maxReplicas,
						},
						Networks: []dockerswarm.NetworkAttachmentConfig{
							{Target: "payments_default"},
							{
								Target:     "payments_public",
								Aliases:    []string{"api"},
								DriverOpts: map[string]string{"encrypted": "true"},
							},
						},
						LogDriver: &dockerswarm.Driver{
							Name: "json-file",
							Options: map[string]string{
								"max-size": "10m",
							},
						},
					},
					Mode: dockerswarm.ServiceMode{
						Replicated: &dockerswarm.ReplicatedService{
							Replicas: &replicas,
						},
					},
					UpdateConfig: &dockerswarm.UpdateConfig{
						Parallelism:     1,
						Delay:           5 * time.Second,
						FailureAction:   "pause",
						Monitor:         1 * time.Minute,
						MaxFailureRatio: 0.25,
						Order:           "start-first",
					},
					RollbackConfig: &dockerswarm.UpdateConfig{
						Parallelism:   2,
						Delay:         3 * time.Second,
						FailureAction: "continue",
						Order:         "stop-first",
					},
					EndpointSpec: &dockerswarm.EndpointSpec{
						Mode: dockerswarm.ResolutionModeVIP,
						Ports: []dockerswarm.PortConfig{
							{
								TargetPort:    8080,
								PublishedPort: 80,
								Protocol:      dockerswarm.PortConfigProtocolTCP,
								PublishMode:   dockerswarm.PortConfigPublishModeIngress,
							},
						},
					},
				},
			},
		},
	}
	networkManager.EXPECT().
		Map(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, ids []string) (map[string]swarm.Network, error) {
			assert.ElementsMatch(t, []string{"payments_default", "payments_public"}, ids)

			return map[string]swarm.Network{
				"payments_default": {
					ID:    "payments_default",
					Name:  "payments_default",
					Stack: "payments",
				},
				"payments_public": {
					ID:    "payments_public",
					Name:  "payments_public",
					Stack: "infra",
				},
			}, nil
		}).
		Times(1)

	computed, err := NewComputer(nil, networkManager).ComputeStack(context.Background(), stack)
	require.NoError(t, err)
	require.NotNil(t, computed)
	require.Len(t, computed.Services, 1)

	service := computed.Services[0]
	assert.Equal(t, "api", service.Name)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", service.Image)
	assert.Equal(t, []string{"/app/api", "--port", "8080"}, service.Command.Args)
	assert.Equal(t, map[string]string{
		"LOG_LEVEL": "debug",
	}, service.Environment.Map)
	assert.Equal(t, map[string]string{"app.env": "prod"}, service.Labels.Map)

	require.NotNil(t, service.Healthcheck)
	assert.Equal(t, []string{"CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"}, service.Healthcheck.Test.Args)
	assert.Equal(t, "10s", service.Healthcheck.Interval)
	assert.Equal(t, "3s", service.Healthcheck.Timeout)
	assert.Equal(t, "5s", service.Healthcheck.StartPeriod)
	assert.Equal(t, "2s", service.Healthcheck.StartInterval)
	require.NotNil(t, service.Healthcheck.Retries)
	assert.Equal(t, uint64(4), *service.Healthcheck.Retries)

	require.Len(t, service.Ports.Ports, 1)
	assert.Equal(t, 80, service.Ports.Ports[0].Published)
	assert.Equal(t, 8080, service.Ports.Ports[0].Target)
	assert.Equal(t, compose.PortProtocolTCP, service.Ports.Ports[0].Protocol)
	assert.Equal(t, "ingress", service.Ports.Ports[0].Mode)

	require.NotNil(t, service.Networks)
	assert.Equal(t, []string{"payments_default", "payments_public"}, service.Networks.GetAliases())
	require.NotNil(t, service.Networks.AliasMap["payments_public"])
	assert.Equal(t, []string{"api"}, service.Networks.AliasMap["payments_public"].Aliases)
	assert.Equal(t, map[string]string{"encrypted": "true"}, service.Networks.AliasMap["payments_public"].DriverOpts)
	require.Len(t, computed.Networks, 1)
	require.Contains(t, computed.Networks, "payments_public")
	assert.Equal(t, "payments_public", computed.Networks["payments_public"].Name)
	require.NotNil(t, computed.Networks["payments_public"].Internal)
	assert.False(t, *computed.Networks["payments_public"].Internal)
	assert.True(t, computed.Networks["payments_public"].External)

	require.Len(t, service.Secrets, 1)
	assert.Equal(t, "db_password", service.Secrets[0].Source)
	assert.Equal(t, "/run/secrets/db_password", service.Secrets[0].Target)

	require.Len(t, service.Configs, 1)
	assert.Equal(t, "api_config", service.Configs[0].Source)
	assert.Equal(t, "/etc/app/config.yml", service.Configs[0].Target)

	require.Len(t, service.Volumes.Volumes, 2)
	require.Contains(t, service.Volumes.Map, "/var/lib/payments")
	require.Contains(t, service.Volumes.Map, "/var/cache/payments")

	bindVolume := service.Volumes.Map["/var/lib/payments"]
	require.NotNil(t, bindVolume)
	assert.Equal(t, compose.ServiceVolumeTypeBind, string(bindVolume.Type))
	assert.Equal(t, "/srv/payments/data", bindVolume.Source)
	assert.Equal(t, "/var/lib/payments", bindVolume.Target)
	assert.True(t, bindVolume.ReadOnly)
	require.NotNil(t, bindVolume.Bind)
	assert.True(t, *bindVolume.Bind.CreateHostPath)
	assert.Equal(t, mount.PropagationRSlave, bindVolume.Bind.Propagation)

	namedVolume := service.Volumes.Map["/var/cache/payments"]
	require.NotNil(t, namedVolume)
	assert.Equal(t, compose.ServiceVolumeTypeVolume, string(namedVolume.Type))
	assert.Equal(t, "payments-cache", namedVolume.Source)
	assert.Equal(t, "/var/cache/payments", namedVolume.Target)
	assert.False(t, namedVolume.ReadOnly)
	require.NotNil(t, namedVolume.Volume)
	assert.True(t, namedVolume.Volume.Nocopy)

	assert.Equal(t, "replicated", service.Deploy.Mode)
	require.NotNil(t, service.Deploy.Replicas)
	assert.Equal(t, uint64(3), *service.Deploy.Replicas)
	assert.Equal(t, "vip", service.Deploy.EndpointMode)
	assert.Equal(t, map[string]string{
		"org.swarm-deploy.service.managed": "true",
	}, service.Deploy.Labels.Map)

	require.NotNil(t, service.Deploy.Resources)
	require.NotNil(t, service.Deploy.Resources.Limits)
	assert.Equal(t, "0.5", service.Deploy.Resources.Limits.Cpus)
	assert.Equal(t, "268435456", service.Deploy.Resources.Limits.Memory)
	require.NotNil(t, service.Deploy.Resources.Limits.Pids)
	assert.Equal(t, uint64(128), *service.Deploy.Resources.Limits.Pids)
	require.NotNil(t, service.Deploy.Resources.Reservations)
	assert.Equal(t, "0.25", service.Deploy.Resources.Reservations.Cpus)
	assert.Equal(t, "134217728", service.Deploy.Resources.Reservations.Memory)

	require.NotNil(t, service.Deploy.RestartPolicy)
	assert.Equal(t, "on-failure", service.Deploy.RestartPolicy.Condition)
	assert.Equal(t, "3s", service.Deploy.RestartPolicy.Delay)
	assert.Equal(t, "30s", service.Deploy.RestartPolicy.Window)
	require.NotNil(t, service.Deploy.RestartPolicy.MaxAttempts)
	assert.Equal(t, uint64(5), *service.Deploy.RestartPolicy.MaxAttempts)

	require.NotNil(t, service.Deploy.UpdateConfig)
	require.NotNil(t, service.Deploy.UpdateConfig.Parallelism)
	assert.Equal(t, uint64(1), *service.Deploy.UpdateConfig.Parallelism)
	assert.Equal(t, "5s", service.Deploy.UpdateConfig.Delay)
	assert.Equal(t, "pause", service.Deploy.UpdateConfig.FailureAction)
	assert.Equal(t, "1m0s", service.Deploy.UpdateConfig.Monitor)
	require.NotNil(t, service.Deploy.UpdateConfig.MaxFailureRatio)
	assert.Equal(t, 0.25, *service.Deploy.UpdateConfig.MaxFailureRatio)
	assert.Equal(t, "start-first", service.Deploy.UpdateConfig.Order)

	require.NotNil(t, service.Deploy.RollbackConfig)
	require.NotNil(t, service.Deploy.RollbackConfig.Parallelism)
	assert.Equal(t, uint64(2), *service.Deploy.RollbackConfig.Parallelism)
	assert.Equal(t, "3s", service.Deploy.RollbackConfig.Delay)
	assert.Equal(t, "continue", service.Deploy.RollbackConfig.FailureAction)
	assert.Equal(t, "stop-first", service.Deploy.RollbackConfig.Order)

	require.NotNil(t, service.Deploy.Placement)
	assert.Equal(t, []string{"node.role == manager"}, service.Deploy.Placement.Constraints)
	require.Len(t, service.Deploy.Placement.Preferences, 1)
	assert.Equal(t, "node.labels.zone", service.Deploy.Placement.Preferences[0].Spread)
	require.NotNil(t, service.Deploy.Placement.MaxReplicasPerNode)
	assert.Equal(t, uint64(2), *service.Deploy.Placement.MaxReplicasPerNode)

	assert.Equal(t, "json-file", service.Logging.Driver)
	assert.Equal(t, map[string]string{"max-size": "10m"}, service.Logging.Options)
}

func TestComputerComputeStackFallsBackToCompactStackService(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networkManager := swarm.NewMockNetworkManager(ctrl)

	replicas := uint64(2)
	stack := Stack{
		Name: "payments",
		Services: []swarm.StackService{
			{
				Name:     "worker",
				Image:    "ghcr.io/swarm-deploy/payments-worker:v4.5.6",
				Mode:     "replicated",
				Replicas: &replicas,
			},
		},
	}
	networkManager.EXPECT().
		Map(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, ids []string) (map[string]swarm.Network, error) {
			assert.Empty(t, ids)
			return map[string]swarm.Network{}, nil
		}).
		Times(1)

	computed, err := NewComputer(nil, networkManager).ComputeStack(context.Background(), stack)
	require.NoError(t, err)
	require.NotNil(t, computed)
	require.Len(t, computed.Services, 1)

	assert.Equal(t, "worker", computed.Services[0].Name)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-worker:v4.5.6", computed.Services[0].Image)
	assert.Equal(t, "replicated", computed.Services[0].Deploy.Mode)
	require.NotNil(t, computed.Services[0].Deploy.Replicas)
	assert.Equal(t, uint64(2), *computed.Services[0].Deploy.Replicas)
}

func TestComputerComputeStackReturnsNetworkManagerError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	networkManager := swarm.NewMockNetworkManager(ctrl)
	networkManager.EXPECT().
		Map(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("swarm unavailable")).
		Times(1)

	stack := Stack{Name: "payments"}

	computed, err := NewComputer(nil, networkManager).ComputeStack(context.Background(), stack)
	require.Error(t, err)
	assert.Nil(t, computed)
	assert.Contains(t, err.Error(), "list networks")
}
