package livemanifest

import (
	"context"
	"errors"
	"os"
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

	environment, err := compose.NewEnvironment([]string{"LOG_LEVEL=debug"})
	require.NoError(t, err)

	assert.Equal(t, &compose.Compose{
		Services: compose.Services{
			{
				Name:    "api",
				Image:   "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				Command: compose.NewCommand([]string{"/app/api", "--port", "8080"}),
				Healthcheck: &compose.ServiceHealth{
					Test:          compose.NewCommand([]string{"CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"}),
					Interval:      "10s",
					Timeout:       "3s",
					Retries:       ptr(uint64(4)),
					StartPeriod:   "5s",
					StartInterval: "2s",
				},
				Ports: compose.ServicePorts{
					Ports: []compose.ServicePort{
						{
							Published: 80,
							Target:    8080,
							Protocol:  compose.PortProtocolTCP,
							Mode:      "ingress",
						},
					},
				},
				Networks: compose.NewServiceNetworks(
					&compose.ServiceNetwork{
						Alias:        "payments_default",
						ResolvedName: "payments_default",
					},
					&compose.ServiceNetwork{
						Alias:        "payments_public",
						ResolvedName: "payments_public",
						Aliases:      []string{"api"},
						DriverOpts:   map[string]string{"encrypted": "true"},
					},
				),
				Secrets: []compose.ObjectRef{
					{
						Source: "db_password",
						Target: "/run/secrets/db_password",
						Mode:   ptr(os.FileMode(0)),
					},
				},
				Configs: []compose.ObjectRef{
					{
						Source: "api_config",
						Target: "/etc/app/config.yml",
						Mode:   ptr(os.FileMode(0)),
					},
				},
				Labels:      *compose.NewLabels(map[string]string{"app.env": "prod"}),
				Environment: *environment,
				Deploy: compose.ServiceDeploy{
					EndpointMode: "vip",
					Labels:       *compose.NewLabels(map[string]string{"org.swarm-deploy.service.managed": "true"}),
					Mode:         "replicated",
					Placement: &compose.ServiceDeployPlacement{
						Constraints: []string{"node.role == manager"},
						Preferences: []compose.ServiceDeployPlacementPreference{
							{Spread: "node.labels.zone"},
						},
						MaxReplicasPerNode: ptr(uint64(2)),
					},
					Replicas: ptr(uint64(3)),
					Resources: &compose.ServiceDeployResources{
						Limits: &compose.ServiceDeployResource{
							Cpus:   "0.5",
							Memory: "268435456",
							Pids:   ptr(uint64(128)),
						},
						Reservations: &compose.ServiceDeployResource{
							Cpus:   "0.25",
							Memory: "134217728",
						},
					},
					RestartPolicy: &compose.ServiceDeployRestartPolicy{
						Condition:   "on-failure",
						Delay:       "3s",
						MaxAttempts: ptr(uint64(5)),
						Window:      "30s",
					},
					RollbackConfig: &compose.ServiceDeployRollbackConfig{
						Parallelism:   ptr(uint64(2)),
						Delay:         "3s",
						FailureAction: "continue",
						Order:         "stop-first",
					},
					UpdateConfig: &compose.ServiceDeployUpdateConfig{
						Parallelism:     ptr(uint64(1)),
						Delay:           "5s",
						FailureAction:   "pause",
						Monitor:         "1m0s",
						MaxFailureRatio: ptr(0.25),
						Order:           "start-first",
					},
				},
				Logging: compose.ServiceLogging{
					Driver:  "json-file",
					Options: map[string]string{"max-size": "10m"},
				},
				Volumes: compose.ServiceVolumes{
					Volumes: []*compose.ServiceVolume{
						{
							Type:     compose.ServiceVolumeTypeBind,
							Source:   "/srv/payments/data",
							Target:   "/var/lib/payments",
							ReadOnly: true,
							Bind: &compose.ServiceVolumeBind{
								CreateHostPath: ptr(true),
								Propagation:    mount.PropagationRSlave,
							},
						},
						{
							Type:   compose.ServiceVolumeTypeVolume,
							Source: "payments-cache",
							Target: "/var/cache/payments",
							Volume: &compose.ServiceVolumeVolume{
								Nocopy:  true,
								Subpath: "api",
							},
						},
					},
					Map: map[string]*compose.ServiceVolume{
						"/var/lib/payments": {
							Type:     compose.ServiceVolumeTypeBind,
							Source:   "/srv/payments/data",
							Target:   "/var/lib/payments",
							ReadOnly: true,
							Bind: &compose.ServiceVolumeBind{
								CreateHostPath: ptr(true),
								Propagation:    mount.PropagationRSlave,
							},
						},
						"/var/cache/payments": {
							Type:   compose.ServiceVolumeTypeVolume,
							Source: "payments-cache",
							Target: "/var/cache/payments",
							Volume: &compose.ServiceVolumeVolume{
								Nocopy:  true,
								Subpath: "api",
							},
						},
					},
				},
			},
		},
		Networks: map[string]compose.Network{
			"payments_public": {
				Name:     "payments_public",
				Internal: ptr(false),
				External: true,
			},
		},
	}, computed)
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
