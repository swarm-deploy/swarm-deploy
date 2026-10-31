package converter

import (
	"context"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
	"go.uber.org/mock/gomock"
)

func TestCommandAndEntrypointShellSemantics(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected []string
	}{
		{`'echo "" end'`, []string{"echo", "", "end"}},
		{`'echo hello # ignored'`, []string{"echo", "hello"}},
		{`'echo "a b" c\ d "#literal"'`, []string{"echo", "a b", "c d", "#literal"}},
		{`[echo, '', 'a b', '#literal', '$VALUE']`, []string{"echo", "", "a b", "#literal", "$VALUE"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			desired := parseStack(t, "services: {api: {image: alpine, command: "+tc.input+", entrypoint: "+tc.input+"}}")
			result, err := convertService("demo", desired.Services[0], nil, []NetworkSpec{{Alias: "default", ID: "network"}}, resources.Resolved{})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result.TaskTemplate.ContainerSpec.Args)
			assert.Equal(t, tc.expected, result.TaskTemplate.ContainerSpec.Command)
		})
	}
}

func TestConvertExtendedServiceOptions(t *testing.T) {
	source := `services:
  api:
    image: alpine:3
    user: '1000:1000'
    working_dir: /app
    hostname: api
    dns: 1.1.1.1
    dns_search: [example.org]
    extra_hosts: ['db:192.0.2.1', 'v6=::1']
    read_only: true
    init: false
    tty: true
    stdin_open: true
    stop_signal: SIGINT
    sysctls: ['net.ipv4.ip_forward=1']
    ulimits: {nofile: {soft: 1024, hard: 2048}, nproc: 256}
    tmpfs: [/run, /tmp]
    isolation: default
    credential_spec: {config: credential}
    oom_score_adj: 100
    deploy:
      resources:
        reservations:
          generic_resources:
            - discrete_resource_spec: {kind: GPU, value: 1}
`
	desired := parseStack(t, source)
	spec, err := convertService("demo", desired.Services[0], nil, []NetworkSpec{{Alias: "default", ID: "network"}}, resources.Resolved{
		Configs: map[string]resources.ResolvedResource{"credential": {ID: "config-id", Name: "rotated-credential"}},
	})
	require.NoError(t, err)
	container := spec.TaskTemplate.ContainerSpec
	assert.Equal(t, "1000:1000", container.User)
	assert.Equal(t, "/app", container.Dir)
	assert.Equal(t, "api", container.Hostname)
	assert.Equal(t, []string{"1.1.1.1"}, container.DNSConfig.Nameservers)
	assert.Equal(t, []string{"example.org"}, container.DNSConfig.Search)
	assert.Equal(t, []string{"192.0.2.1 db", "::1 v6"}, container.Hosts)
	assert.True(t, container.ReadOnly)
	require.NotNil(t, container.Init)
	assert.False(t, *container.Init)
	assert.True(t, container.TTY)
	assert.True(t, container.OpenStdin)
	assert.Equal(t, "SIGINT", container.StopSignal)
	assert.Equal(t, map[string]string{"net.ipv4.ip_forward": "1"}, container.Sysctls)
	require.Len(t, container.Ulimits, 2)
	assert.Equal(t, int64(1024), container.Ulimits[0].Soft)
	assert.Equal(t, int64(2048), container.Ulimits[0].Hard)
	assert.Equal(t, int64(256), container.Ulimits[1].Soft)
	require.Len(t, container.Mounts, 2)
	assert.Equal(t, "/run", container.Mounts[0].Target)
	assert.EqualValues(t, "default", container.Isolation)
	assert.Equal(t, int64(100), container.OomScoreAdj)
	assert.Equal(t, "config-id", container.Privileges.CredentialSpec.Config)
	require.Len(t, container.Configs, 1)
	assert.NotNil(t, container.Configs[0].Runtime)
	assert.Nil(t, container.Configs[0].File)
	assert.Equal(t, []dockerswarm.GenericResource{{DiscreteResourceSpec: &dockerswarm.DiscreteGenericResource{Kind: "GPU", Value: 1}}}, spec.TaskTemplate.Resources.Reservations.GenericResources)
}

func TestJobModes(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job"} {
		t.Run(mode, func(t *testing.T) {
			desired := parseStack(t, "services: {api: {image: alpine, deploy: {mode: "+mode+"}}}")
			spec, err := convertService("demo", desired.Services[0], nil, []NetworkSpec{{Alias: "default", ID: "network"}}, resources.Resolved{})
			require.NoError(t, err)
			if mode == "replicated-job" {
				assert.NotNil(t, spec.Mode.ReplicatedJob)
			} else {
				assert.NotNil(t, spec.Mode.GlobalJob)
			}
		})
	}
}

func TestOnlyReferencedNetworksAndIPAM(t *testing.T) {
	desired := parseStack(t, `services:
  api: {image: alpine, x-init-deploy-jobs: [{name: prepare, image: alpine, networks: [jobs]}]}
networks:
  default:
    ipam:
      driver: default
      options: {foo: bar}
      config:
        - subnet: 10.12.0.0/24
          ip_range: 10.12.0.0/25
          gateway: 10.12.0.1
          aux_addresses: {reserved: 10.12.0.2}
  jobs: {}
  unused_owned: {}
  unused_external: {external: true, name: missing}
`)
	converter, networks, _, _ := newTestConverter(t)
	plan, err := converter.PlanNetworks("demo", desired)
	require.NoError(t, err)
	require.Len(t, plan, 2)
	assert.Equal(t, "default", plan[0].Alias)
	assert.Equal(t, "jobs", plan[1].Alias)
	require.NotNil(t, plan[0].Spec.IPAM)
	assert.Equal(t, "10.12.0.0/24", plan[0].Spec.IPAM.Config[0].Subnet)
	assert.Equal(t, "10.12.0.0/25", plan[0].Spec.IPAM.Config[0].IPRange)
	assert.Equal(t, "10.12.0.1", plan[0].Spec.IPAM.Config[0].Gateway)
	assert.Equal(t, map[string]string{"reserved": "10.12.0.2"}, plan[0].Spec.IPAM.Config[0].AuxAddress)
	assert.Equal(t, map[string]string{"foo": "bar"}, plan[0].Spec.IPAM.Options)
	for _, name := range []string{"demo_default", "demo_jobs"} {
		networks.EXPECT().Get(gomock.Any(), name).Return(existingNetwork(name), nil)
	}
	_, err = converter.Convert(context.Background(), "demo", desired)
	require.NoError(t, err)
}

func TestEnvironmentConverterDoesNotResolveProcessValues(t *testing.T) {
	t.Setenv("BARE", "hidden")
	desired, err := compose.Parse([]byte(`services: {api: {image: alpine, environment: [BARE, 'EMPTY=', 'LITERAL=null']}}`))
	require.NoError(t, err)
	spec, err := convertService("demo", desired.Services[0], nil, []NetworkSpec{{Alias: "default", ID: "network"}}, resources.Resolved{})
	require.NoError(t, err)
	assert.Equal(t, []string{"BARE", "EMPTY=", "LITERAL=null"}, spec.TaskTemplate.ContainerSpec.Env)
}

func TestTmpfsShortOptions(t *testing.T) {
	spec, err := serviceTmpfsMount("/run:size=64m,mode=1777,ro")
	require.NoError(t, err)
	assert.Equal(t, int64(64*1024*1024), spec.TmpfsOptions.SizeBytes)
	assert.EqualValues(t, 01777, spec.TmpfsOptions.Mode)
	assert.True(t, spec.ReadOnly)
	for _, value := range []string{"relative", "/run:size=bad", "/run:mode=8888", "/run:unknown=1"} {
		_, err = serviceTmpfsMount(value)
		require.Error(t, err)
	}
}

func TestExtraHostsPreservesListOrder(t *testing.T) {
	desired := parseStack(t, `services: {api: {image: alpine, extra_hosts: ['z:192.0.2.1','a:192.0.2.2','z:192.0.2.3']}}`)
	spec, err := convertService("demo", desired.Services[0], nil, []NetworkSpec{{Alias: "default", ID: "net"}}, resources.Resolved{})
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.1 z", "192.0.2.2 a", "192.0.2.3 z"}, spec.TaskTemplate.ContainerSpec.Hosts)
}

func TestInvalidExtendedOptionsFailClearly(t *testing.T) {
	for _, tc := range []struct{ option, field string }{
		{"oom_score_adj: 1001", "oom_score_adj"},
		{"isolation: invalid", "isolation"},
		{"ulimits: {nofile: {soft: 20, hard: 10}}", "ulimits.nofile"},
		{"credential_spec: {file: file, registry: registry}", "credential_spec"},
		{"credential_spec: {config: missing}", "credential_spec.config"},
		{"deploy: {mode: global-job, replicas: 1}", "deploy.replicas"},
		{"deploy: {resources: {reservations: {generic_resources: [{discrete_resource_spec: {kind: GPU, value: -1}}]}}}", "generic_resources"},
	} {
		t.Run(tc.field+tc.option, func(t *testing.T) {
			desired := parseStack(t, "services: {api: {image: alpine, "+tc.option+"}}")
			_, err := convertService("demo", desired.Services[0], nil, []NetworkSpec{{Alias: "default", ID: "net"}}, resources.Resolved{})
			require.ErrorContains(t, err, tc.field)
		})
	}
}
