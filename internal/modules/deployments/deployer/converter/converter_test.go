package converter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func newTestConverter(t *testing.T) (*Converter, *swarm.MockNetworkManager, *swarm.MockConfigManager, *swarm.MockSecretManager) {
	t.Helper()
	ctrl := gomock.NewController(t)
	networks, configs, secrets := swarm.NewMockNetworkManager(ctrl), swarm.NewMockConfigManager(ctrl), swarm.NewMockSecretManager(ctrl)
	return New(networks, configs, secrets), networks, configs, secrets
}

func parseStack(t *testing.T, yaml string) compose.Compose {
	t.Helper()
	stack, err := compose.Parse([]byte(yaml))
	require.NoError(t, err)
	return *stack
}

func existingNetwork(name string) swarm.Network {
	return swarm.Network{ID: "id-" + name, Name: name, Scope: "swarm"}
}

func TestConvertMinimalAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		services   int
	}{
		{"empty", "{}", 0}, {"minimal", "services:\n  app:\n    image: nginx:alpine\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter, networks, _, _ := newTestConverter(t)
			if tc.services > 0 {
				networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
			}
			result, err := converter.Convert(context.Background(), "demo", parseStack(t, tc.yaml))
			require.NoError(t, err)
			require.Len(t, result.Services, tc.services)
			if tc.services == 0 {
				assert.Empty(t, result.Networks)
				return
			}
			service := result.Services[0]
			assert.Equal(t, "demo_app", service.Name)
			assert.Equal(t, map[string]string{"com.docker.stack.namespace": "demo", "com.docker.stack.image": "nginx:alpine"}, service.Labels)
			assert.Equal(t, map[string]string{"com.docker.stack.namespace": "demo"}, service.TaskTemplate.ContainerSpec.Labels)
			require.NotNil(t, service.Mode.Replicated)
			assert.Nil(t, service.Mode.Replicated.Replicas) // Docker supplies the one-replica default.
			assert.Nil(t, service.TaskTemplate.RestartPolicy)
			assert.Nil(t, service.UpdateConfig)
			assert.Nil(t, service.RollbackConfig)
			assert.Equal(t, []dockerswarm.NetworkAttachmentConfig{{Target: "id-demo_default", Aliases: []string{"app"}}}, service.TaskTemplate.Networks)
			assert.Equal(t, "overlay", result.Networks[0].Spec.Driver)
		})
	}
}

const completeStack = `
version: '3.8'
x-meta: {owner: test}
services:
  web:
    image: nginx:alpine
    entrypoint: ['/entrypoint']
    command: serve --message "hello world"
    environment: {Z: last, A: first}
    cap_add: [net_admin]
    cap_drop: [ALL]
    labels: {container: web}
    healthcheck:
      test: curl -f localhost
      interval: 5s
      timeout: 2s
      start_period: 10s
      start_interval: 1s
      retries: 3
    ports:
      - {target: 80, published: 8080}
      - {target: 53, published: 5353, protocol: udp, mode: host}
    networks:
      backend: {aliases: [api], driver_opts: {foo: bar}}
      public: {}
    configs:
      - source: settings
        target: /etc/settings
        uid: '1000'
        gid: '1001'
        mode: 0440
      - external-config
    secrets:
      - token
      - source: external-secret
        target: custom
        uid: '1002'
        gid: '1003'
        mode: 0400
    volumes:
      - data:/data:ro
      - external-volume:/external
      - /cache
      - type: bind
        source: /srv/data
        target: /bind
        read_only: true
        bind: {propagation: rslave}
      - type: tmpfs
        target: /tmp
        tmpfs: {size: 64m, mode: 0700}
    logging: {driver: json-file, options: {max-size: 10m}}
    deploy:
      replicas: 3
      labels: {service: web}
      endpoint_mode: vip
      placement:
        constraints: [node.role == worker]
        preferences: [{spread: node.labels.zone}]
        max_replicas_per_node: 2
      resources:
        limits: {cpus: '1.5', memory: 256m, pids: 100}
        reservations: {cpus: '0.25', memory: 64m}
      restart_policy: {condition: on-failure, delay: 2s, window: 1m, max_attempts: 5}
      update_config: {parallelism: 0, delay: 1s, monitor: 5s, failure_action: rollback, max_failure_ratio: 0.25, order: start-first}
      rollback_config: {delay: 2s, monitor: 10s, failure_action: pause, order: stop-first}
    x-init-deploy-jobs:
      - name: migrate
        image: alpine
        command: [true]
        networks: [backend]
  agent:
    image: alpine:3
    networks: [backend]
    deploy: {mode: global}
networks:
  backend:
    name: shared-backend
    driver: overlay
    driver_opts: {encrypted: ''}
    attachable: true
    internal: true
    labels: {team: platform}
  public: {external: true, name: edge}
volumes:
  data:
    driver: local
    driver_opts: {type: tmpfs, device: tmpfs}
    labels: {volume: data}
  external-volume: {external: true, name: existing-volume}
configs:
  settings: {name: demo_settings_abc123, file: settings.yaml}
  external-config: {external: true}
secrets:
  token: {name: demo_token_def456, file: token.age}
  external-secret: {external: true, name: global-token}
`

func expectCompleteResources(networks *swarm.MockNetworkManager, configs *swarm.MockConfigManager, secrets *swarm.MockSecretManager, times int) {
	networks.EXPECT().Get(gomock.Any(), "shared-backend").Return(existingNetwork("shared-backend"), nil).Times(times)
	networks.EXPECT().Get(gomock.Any(), "edge").Return(existingNetwork("edge"), nil).Times(times)
	configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{"external-config", "demo_settings_abc123"}}).
		Return([]swarm.Config{{ID: "settings-id", Name: "demo_settings_abc123", Data: []byte("config-payload")}, {ID: "external-config-id", Name: "external-config"}}, nil).Times(times)
	secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"global-token", "demo_token_def456"}}).
		Return([]swarm.Secret{{ID: "token-id", Name: "demo_token_def456"}, {ID: "external-secret-id", Name: "global-token"}}, nil).Times(times)
}

func TestConvertCompleteStack(t *testing.T) {
	converter, networks, configs, secrets := newTestConverter(t)
	expectCompleteResources(networks, configs, secrets, 1)
	desired := parseStack(t, completeStack)
	result, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	require.Len(t, result.Services, 2) // Init Jobs never become persistent services.
	require.Len(t, result.Networks, 2)
	assert.NotNil(t, result.Services[0].Mode.Global)
	web := result.Services[1]
	container := web.TaskTemplate.ContainerSpec
	assert.Equal(t, "demo_web", web.Name)
	assert.Equal(t, "web", web.Labels["service"])
	assert.Equal(t, "web", container.Labels["container"])
	assert.Equal(t, []string{"/entrypoint"}, container.Command)
	assert.Equal(t, []string{"serve", "--message", "hello world"}, container.Args)
	assert.Equal(t, []string{"A=first", "Z=last"}, container.Env)
	assert.Equal(t, []string{"CAP_NET_ADMIN"}, container.CapabilityAdd)
	assert.Equal(t, []string{"ALL"}, container.CapabilityDrop)
	assert.Equal(t, []string{"CMD-SHELL", "curl -f localhost"}, container.Healthcheck.Test)
	assert.Equal(t, 5*time.Second, container.Healthcheck.Interval)
	assert.Equal(t, 2*time.Second, container.Healthcheck.Timeout)
	assert.Equal(t, 10*time.Second, container.Healthcheck.StartPeriod)
	assert.Equal(t, time.Second, container.Healthcheck.StartInterval)
	assert.Equal(t, 3, container.Healthcheck.Retries)
	assert.Equal(t, uint64(3), *web.Mode.Replicated.Replicas)
	assert.Equal(t, &dockerswarm.Limit{NanoCPUs: 1_500_000_000, MemoryBytes: 256 * 1024 * 1024, Pids: 100}, web.TaskTemplate.Resources.Limits)
	assert.Equal(t, &dockerswarm.Resources{NanoCPUs: 250_000_000, MemoryBytes: 64 * 1024 * 1024}, web.TaskTemplate.Resources.Reservations)
	assert.Equal(t, []string{"node.role == worker"}, web.TaskTemplate.Placement.Constraints)
	assert.Equal(t, "node.labels.zone", web.TaskTemplate.Placement.Preferences[0].Spread.SpreadDescriptor)
	assert.Equal(t, uint64(2), web.TaskTemplate.Placement.MaxReplicas)
	assert.Equal(t, dockerswarm.RestartPolicyConditionOnFailure, web.TaskTemplate.RestartPolicy.Condition)
	assert.Equal(t, 2*time.Second, *web.TaskTemplate.RestartPolicy.Delay)
	assert.Equal(t, time.Minute, *web.TaskTemplate.RestartPolicy.Window)
	assert.Equal(t, uint64(5), *web.TaskTemplate.RestartPolicy.MaxAttempts)
	assert.Equal(t, &dockerswarm.UpdateConfig{Parallelism: 0, Delay: time.Second, Monitor: 5 * time.Second,
		FailureAction: "rollback", MaxFailureRatio: 0.25, Order: "start-first"}, web.UpdateConfig)
	assert.Equal(t, &dockerswarm.UpdateConfig{Parallelism: 1, Delay: 2 * time.Second, Monitor: 10 * time.Second,
		FailureAction: "pause", Order: "stop-first"}, web.RollbackConfig)
	assert.Equal(t, dockerswarm.ResolutionModeVIP, web.EndpointSpec.Mode)
	assert.Equal(t, []dockerswarm.PortConfig{
		{Protocol: "udp", TargetPort: 53, PublishedPort: 5353, PublishMode: "host"},
		{Protocol: "tcp", TargetPort: 80, PublishedPort: 8080, PublishMode: "ingress"},
	}, web.EndpointSpec.Ports)
	assert.Equal(t, &dockerswarm.Driver{Name: "json-file", Options: map[string]string{"max-size": "10m"}}, web.TaskTemplate.LogDriver)
	assert.Equal(t, "id-edge", web.TaskTemplate.Networks[0].Target)
	assert.Equal(t, []string{"api", "web"}, web.TaskTemplate.Networks[1].Aliases)
	assert.Equal(t, map[string]string{"foo": "bar"}, web.TaskTemplate.Networks[1].DriverOpts)
	backend := result.Networks[0]
	assert.Equal(t, "backend", backend.Alias)
	assert.Equal(t, "shared-backend", backend.Spec.Name)
	assert.True(t, backend.Spec.Internal)
	assert.True(t, backend.Spec.Attachable)
	assert.Equal(t, map[string]string{"encrypted": ""}, backend.Spec.Options)
	assert.Equal(t, map[string]string{"team": "platform", "com.docker.stack.namespace": "demo"}, backend.Spec.Labels)
	assert.True(t, result.Networks[1].External)
	assert.Nil(t, result.Networks[1].Spec.Labels)
	require.Len(t, container.Configs, 2)
	assert.Equal(t, &dockerswarm.ConfigReference{ConfigID: "settings-id", ConfigName: "demo_settings_abc123",
		File: &dockerswarm.ConfigReferenceFileTarget{Name: "/etc/settings", UID: "1000", GID: "1001", Mode: 0o440}}, container.Configs[0])
	assert.Equal(t, "/external-config", container.Configs[1].File.Name)
	assert.Equal(t, "external-config-id", container.Configs[1].ConfigID)
	require.Len(t, container.Secrets, 2)
	assert.Equal(t, &dockerswarm.SecretReference{SecretID: "token-id", SecretName: "demo_token_def456",
		File: &dockerswarm.SecretReferenceFileTarget{Name: "token", UID: "0", GID: "0", Mode: 0o444}}, container.Secrets[0])
	assert.Equal(t, &dockerswarm.SecretReference{SecretID: "external-secret-id", SecretName: "global-token",
		File: &dockerswarm.SecretReferenceFileTarget{Name: "custom", UID: "1002", GID: "1003", Mode: 0o400}}, container.Secrets[1])
	require.Len(t, container.Mounts, 5)
	assert.Equal(t, "demo_data", container.Mounts[0].Source)
	assert.True(t, container.Mounts[0].ReadOnly)
	assert.Equal(t, "local", container.Mounts[0].VolumeOptions.DriverConfig.Name)
	assert.Equal(t, "demo", container.Mounts[0].VolumeOptions.Labels["com.docker.stack.namespace"])
	assert.Equal(t, "existing-volume", container.Mounts[1].Source)
	assert.Nil(t, container.Mounts[1].VolumeOptions.DriverConfig)
	assert.Nil(t, container.Mounts[1].VolumeOptions.Labels)
	assert.Empty(t, container.Mounts[2].Source)
	assert.Equal(t, mount.TypeVolume, container.Mounts[2].Type)
	assert.Equal(t, mount.PropagationRSlave, container.Mounts[3].BindOptions.Propagation)
	assert.Equal(t, &mount.TmpfsOptions{SizeBytes: 64 * 1024 * 1024, Mode: 0o700}, container.Mounts[4].TmpfsOptions)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "config-payload")
}

func TestConvertDeterministicAndIndependent(t *testing.T) {
	converter, networks, configs, secrets := newTestConverter(t)
	expectCompleteResources(networks, configs, secrets, 2)
	desired := parseStack(t, completeStack)
	original := parseStack(t, completeStack)
	first, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	// Reverse input service order; map insertion order must not affect specs either.
	desired.Services[0], desired.Services[1] = desired.Services[1], desired.Services[0]
	second, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	desired.Services[0], desired.Services[1] = desired.Services[1], desired.Services[0]
	assert.Equal(t, original, desired)
	web := first.Services[1]
	web.Labels["service"] = "changed"
	web.TaskTemplate.ContainerSpec.Labels["container"] = "changed"
	web.TaskTemplate.Networks[1].Aliases[0] = "changed"
	web.TaskTemplate.Networks[1].DriverOpts["foo"] = "changed"
	web.TaskTemplate.ContainerSpec.Command[0] = "changed"
	web.TaskTemplate.Placement.Constraints[0] = "changed"
	*web.Mode.Replicated.Replicas = 42
	*web.TaskTemplate.RestartPolicy.MaxAttempts = 42
	web.TaskTemplate.ContainerSpec.Mounts[0].VolumeOptions.DriverConfig.Options["type"] = "changed"
	first.Networks[0].Spec.Options["encrypted"] = "changed"
	first.Networks[0].Spec.Labels["team"] = "changed"
	assert.Equal(t, original, desired, "returned specs must not share mutable input data")
}

func TestConvertMissingResourcesAndReadErrors(t *testing.T) {
	for _, tc := range []struct{ name, yaml, field string }{
		{"network", "services: {app: {image: nginx}}", "networks.default"},
		{"config", "configs: {settings: {external: true}}", "configs.settings"},
		{"secret", "secrets: {token: {external: true}}", "secrets.token"},
	} {
		for _, fail := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: " missing", true: " API error"}[fail], func(t *testing.T) {
				converter, networks, configs, secrets := newTestConverter(t)
				var apiErr error
				if fail {
					apiErr = errors.New("sensitive-payload")
				}
				switch tc.name {
				case "network":
					if !fail {
						apiErr = swarm.ErrNetworkNotFound
					}
					networks.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{}, apiErr)
				case "config":
					configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{"settings"}}).Return(nil, apiErr)
				case "secret":
					secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"token"}}).Return(nil, apiErr)
				}
				result, err := converter.Convert(context.Background(), "demo", parseStack(t, tc.yaml))
				require.Error(t, err)
				assert.Nil(t, result)
				if fail && tc.name != "network" {
					assert.Contains(t, err.Error(), tc.name+"s:")
				} else {
					assert.Contains(t, err.Error(), tc.field)
				}
				assert.NotContains(t, err.Error(), "sensitive-payload")
			})
		}
	}
}

func TestConvertExactResourceNames(t *testing.T) {
	converter, _, configs, secrets := newTestConverter(t)
	configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{"demo_settings"}}).
		Return([]swarm.Config{{ID: "wrong", Name: "demo_settings_old"}}, nil)
	result, err := converter.Convert(context.Background(), "demo", parseStack(t, "configs: {settings: {}}"))
	require.ErrorContains(t, err, "configs.settings")
	assert.Nil(t, result)
	// Docker name filters are prefix filters. Never adopt a similarly named resource.
	secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"demo_token"}}).
		Return([]swarm.Secret{{ID: "wrong", Name: "demo_token_old"}}, nil)
	_, err = converter.Convert(context.Background(), "demo", parseStack(t, "secrets: {token: {}}"))
	require.ErrorContains(t, err, "secrets.token")
}

func TestConvertDefaultExternalNamesAndLookupReuse(t *testing.T) {
	converter, networks, configs, secrets := newTestConverter(t)
	desired := parseStack(t, `
services:
 a: {image: alpine, networks: [one], configs: [cfg], secrets: [key], volumes: [data:/data]}
 b: {image: alpine, networks: [two], configs: [cfg], secrets: [key]}
networks: {one: {external: true, name: shared}, two: {external: true, name: shared}}
configs: {cfg: {}}
secrets: {key: {}}
volumes: {data: {external: true}}
`)
	networks.EXPECT().Get(gomock.Any(), "shared").Return(existingNetwork("shared"), nil)
	configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{"demo_cfg"}}).
		Return([]swarm.Config{{ID: "config-id", Name: "demo_cfg"}}, nil)
	secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"demo_key"}}).
		Return([]swarm.Secret{{ID: "secret-id", Name: "demo_key"}}, nil)
	result, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	assert.Equal(t, "data", result.Services[0].TaskTemplate.ContainerSpec.Mounts[0].Source)
	for _, service := range result.Services {
		assert.Equal(t, "config-id", service.TaskTemplate.ContainerSpec.Configs[0].ConfigID)
		assert.Equal(t, "secret-id", service.TaskTemplate.ContainerSpec.Secrets[0].SecretID)
	}
}

func TestConvertNoDockerWritesOrDirectClient(t *testing.T) {
	// Generated manager mocks reject every unregistered operation, including writes.
	// The converter has no client dependency; check production imports against future regressions.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, readErr := os.ReadFile(entry.Name())
		require.NoError(t, readErr)
		assert.NotContains(t, string(data), `"github.com/docker/docker/client"`, entry.Name())
		assert.NotContains(t, string(data), "os/exec", entry.Name())
	}
}

func TestConvertOptionalContainerAndMountSettings(t *testing.T) {
	converter, networks, _, _ := newTestConverter(t)
	networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
	desired := parseStack(t, `
services:
 app:
  image: alpine
  command: [echo, 'two words']
  entrypoint: /bin/sh -c
  healthcheck: {disable: true}
  cap_add: [NET_ADMIN, cap_net_admin, RESET]
  cap_drop: [net_admin, sys_admin]
  ports: [{target: 80, mode: host}]
  networks: [default]
  deploy: {replicas: 0, endpoint_mode: dnsrr, update_config: {}, rollback_config: {}, restart_policy: {}}
  volumes:
   - type: volume
     source: data
     target: /data
     volume: {nocopy: true, subpath: subdir}
   - type: volume
     target: /anonymous
     volume: {nocopy: true}
   - type: tmpfs
     target: /tmp
     tmpfs: {mode: 01777}
volumes: {data: {name: explicitly-named}}
`)
	result, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	spec := result.Services[0]
	container := spec.TaskTemplate.ContainerSpec
	assert.Equal(t, []string{"echo", "two words"}, container.Args)
	assert.Equal(t, []string{"/bin/sh", "-c"}, container.Command)
	assert.Equal(t, []string{"NONE"}, container.Healthcheck.Test)
	assert.Equal(t, []string{"CAP_NET_ADMIN"}, container.CapabilityAdd)
	assert.Equal(t, []string{"CAP_SYS_ADMIN"}, container.CapabilityDrop)
	assert.Equal(t, uint64(0), *spec.Mode.Replicated.Replicas)
	assert.Equal(t, uint64(1), spec.UpdateConfig.Parallelism)
	assert.Equal(t, uint64(1), spec.RollbackConfig.Parallelism)
	assert.Nil(t, spec.TaskTemplate.RestartPolicy.Delay)
	assert.Nil(t, spec.TaskTemplate.RestartPolicy.Window)
	assert.Equal(t, "explicitly-named", container.Mounts[0].Source)
	assert.True(t, container.Mounts[0].VolumeOptions.NoCopy)
	assert.Equal(t, "subdir", container.Mounts[0].VolumeOptions.Subpath)
	assert.True(t, container.Mounts[1].VolumeOptions.NoCopy)
	assert.Equal(t, os.FileMode(0o1777), container.Mounts[2].TmpfsOptions.Mode)
}

func TestConvertInitJobNetworkDependencies(t *testing.T) {
	converter, networks, _, _ := newTestConverter(t)
	networks.EXPECT().Get(gomock.Any(), "demo_backend").Return(existingNetwork("demo_backend"), nil)
	networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
	desired := parseStack(t, `
services:
 app:
  image: alpine
  networks: [backend]
  x-init-deploy-jobs: [{name: migration, image: alpine, networks: [default]}]
networks: {backend: {}}
`)
	result, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	require.Len(t, result.Networks, 2)
	require.Len(t, result.Services, 1)
	assert.Equal(t, "id-demo_backend", result.Services[0].TaskTemplate.Networks[0].Target)
}

func TestConvertRejectsIncorrectNetworkMetadata(t *testing.T) {
	for _, network := range []swarm.Network{
		{ID: "id", Name: "wrong", Scope: "swarm"},
		{ID: "id", Name: "demo_default", Scope: "local"},
		{Name: "demo_default", Scope: "swarm"},
	} {
		t.Run(network.ID+network.Name+network.Scope, func(t *testing.T) {
			converter, networks, _, _ := newTestConverter(t)
			networks.EXPECT().Get(gomock.Any(), "demo_default").Return(network, nil)
			_, err := converter.Convert(context.Background(), "demo", parseStack(t, "services: {app: {image: alpine}}"))
			require.ErrorContains(t, err, "networks.default")
		})
	}
}

func TestConvertPreservesObjectRefSyntaxThroughJSON(t *testing.T) {
	converter, networks, configs, secrets := newTestConverter(t)
	networks.EXPECT().Get(gomock.Any(), "demo_default").
		Return(existingNetwork("demo_default"), nil).Times(2)
	configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{"demo_explicit", "demo_settings"}}).
		Return([]swarm.Config{
			{ID: "config-a", Name: "demo_settings"},
			{ID: "config-b", Name: "demo_explicit"},
		}, nil).Times(2)
	secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"demo_token"}}).
		Return([]swarm.Secret{{ID: "secret-a", Name: "demo_token"}}, nil).Times(2)

	desired := parseStack(t, `
services:
  app:
    image: alpine
    configs:
      - settings
      - {source: explicit, target: /run/secrets/explicit}
    secrets:
      - token
configs: {settings: {}, explicit: {}}
secrets: {token: {}}
`)
	require.True(t, desired.Services[0].Configs[0].IsShortForm())
	require.False(t, desired.Services[0].Configs[1].IsShortForm())
	before, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)

	payload, err := json.Marshal(desired)
	require.NoError(t, err)
	var restored compose.Compose
	require.NoError(t, json.Unmarshal(payload, &restored))
	require.True(t, restored.Services[0].Configs[0].IsShortForm())
	require.False(t, restored.Services[0].Configs[1].IsShortForm())

	after, err := converter.Convert(context.Background(), "demo", restored)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	require.Len(t, after.Services[0].TaskTemplate.ContainerSpec.Configs, 2)
	byName := map[string]string{}
	for _, ref := range after.Services[0].TaskTemplate.ContainerSpec.Configs {
		byName[ref.ConfigName] = ref.File.Name
	}
	assert.Equal(t, "/settings", byName["demo_settings"])
	assert.Equal(t, "/run/secrets/explicit", byName["demo_explicit"])
	require.Len(t, after.Services[0].TaskTemplate.ContainerSpec.Secrets, 1)
	assert.Equal(t, "token", after.Services[0].TaskTemplate.ContainerSpec.Secrets[0].File.Name)
}

func TestConvertStopGracePeriod(t *testing.T) {
	thirtySeconds := 30 * time.Second
	zero := time.Duration(0)
	for _, tc := range []struct {
		name, value string
		expected    *time.Duration
		error       bool
	}{
		{name: "unspecified"},
		{name: "thirty seconds", value: "30s", expected: &thirtySeconds},
		{name: "explicit zero", value: "0s", expected: &zero},
		{name: "invalid duration", value: "invalid", error: true},
		{name: "negative duration", value: "-1s", error: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter, networks, _, _ := newTestConverter(t)
			networks.EXPECT().Get(gomock.Any(), "demo_default").
				Return(existingNetwork("demo_default"), nil)
			desired := parseStack(t, "services: {app: {image: alpine, stop_grace_period: '"+tc.value+"'}}")
			result, err := converter.Convert(context.Background(), "demo", desired)
			if tc.error {
				require.ErrorContains(t, err, "stop_grace_period")
				assert.Nil(t, result)
				return
			}
			require.NoError(t, err)
			actual := result.Services[0].TaskTemplate.ContainerSpec.StopGracePeriod
			if tc.expected == nil {
				assert.Nil(t, actual)
			} else {
				require.NotNil(t, actual)
				assert.Equal(t, *tc.expected, *actual)
			}
		})
	}
}

func TestConvertProjectComposeExample(t *testing.T) {
	// Read the actual repository example to catch unsupported fields in supported Swarm options.
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "example", "01-basic", "docker-compose.yaml"))
	require.NoError(t, err)
	desired := parseStack(t, string(raw))
	require.Len(t, desired.Services, 1)
	// The legacy service-level restart option is ignored by docker stack deploy.
	// It is deliberately still rejected by the strict converter; remove only that option.
	require.Contains(t, desired.Services[0].Extra, "restart")
	delete(desired.Services[0].Extra, "restart")

	converter, networks, configs, _ := newTestConverter(t)
	networks.EXPECT().Get(gomock.Any(), "demo_default").
		Return(existingNetwork("demo_default"), nil)
	configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{"demo_swarm-deploy"}}).
		Return([]swarm.Config{{ID: "cfg-1", Name: "demo_swarm-deploy"}}, nil)

	result, err := converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
	require.Len(t, result.Services, 1)
	spec := result.Services[0]
	require.NotNil(t, spec.TaskTemplate.ContainerSpec.StopGracePeriod)
	assert.Equal(t, 30*time.Second, *spec.TaskTemplate.ContainerSpec.StopGracePeriod)
	assert.Equal(t, "demo_swarm-deploy", spec.Name)
	require.Len(t, spec.TaskTemplate.ContainerSpec.Configs, 1)
	assert.Equal(t, "cfg-1", spec.TaskTemplate.ContainerSpec.Configs[0].ConfigID)
}
