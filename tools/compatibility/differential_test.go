package compatibility

import (
	"context"
	"encoding/json"
	"testing"

	cliconvert "github.com/docker/cli/cli/compose/convert"
	cliloader "github.com/docker/cli/cli/compose/loader"
	clitypes "github.com/docker/cli/cli/compose/types"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
	"go.yaml.in/yaml/v3"
)

// The independent loader/converter is pinned to Docker CLI v28.3.3 in go.mod.
// It is used only in tests and never loads prepared production snapshots.
func TestDockerCLIDifferential(t *testing.T) {
	for _, fixture := range []string{
		`command: 'echo "" end'`,
		`entrypoint: 'echo hello # ignored'`,
		`command: [echo, '', 'a b', '$VALUE']`,
		`user: '1000:1000'
    working_dir: /app
    hostname: web
    dns: [1.1.1.1]
    dns_search: example.org
    extra_hosts: ['db:192.0.2.1']
    read_only: true
    init: true
    tty: true
    stdin_open: true
    stop_signal: SIGINT
    sysctls: {net.ipv4.ip_forward: '1'}
    ulimits: {nofile: {soft: 1024, hard: 2048}, nproc: 256}
    isolation: default`,
		`ports: ['8000-8002:8000-8002/udp']`,
		`deploy: {mode: replicated-job, replicas: 3}`,
		`deploy: {mode: global-job}`,
		`deploy: {resources: {reservations: {generic_resources: [{discrete_resource_spec: {kind: GPU, value: 1}}]}}}`,
		`credential_spec: {file: credentials.json}`,
	} {
		t.Run(fixture, func(t *testing.T) {
			source := "version: '3.8'\nservices:\n  api:\n    image: alpine:3\n    " + fixture + "\n"
			var dictionary map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(source), &dictionary))
			cli, err := cliloader.Load(clitypes.ConfigDetails{WorkingDir: t.TempDir(), ConfigFiles: []clitypes.ConfigFile{{Config: dictionary}}}, func(options *cliloader.Options) { options.SkipInterpolation = true })
			require.NoError(t, err)
			expected, err := cliconvert.Service("1.51", cliconvert.NewNamespace("demo"), cli.Services[0], cli.Networks, cli.Volumes, nil, nil)
			require.NoError(t, err)
			native, err := compose.Parse([]byte(source))
			require.NoError(t, err)
			ctrl := gomock.NewController(t)
			networks := swarm.NewMockNetworkManager(ctrl)
			networks.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{ID: "demo_default", Name: "demo_default", Scope: "swarm"}, nil)
			converted, err := converter.New(networks, swarm.NewMockConfigManager(ctrl), swarm.NewMockSecretManager(ctrl)).Convert(t.Context(), "demo", *native)
			require.NoError(t, err)
			actual := converted.Services[0]
			require.NoError(t, err)
			// CLI always emits empty optional objects; normalize Docker defaults and
			// compare effective API values instead of nil versus empty collections.
			expected.TaskTemplate.Networks = actual.TaskTemplate.Networks
			assert.Equal(t, canonicalDifferential(expected), canonicalDifferential(actual))
		})
	}
}

func canonicalDifferential(spec dockerswarm.ServiceSpec) any {
	// Differences that don't change the API meaning are collapsed here, without
	// applying native image normalization or copying any converted service fields.
	if spec.TaskTemplate.Resources == nil {
		spec.TaskTemplate.Resources = &dockerswarm.ResourceRequirements{}
	}
	if spec.TaskTemplate.Placement == nil {
		spec.TaskTemplate.Placement = &dockerswarm.Placement{}
	}
	if spec.TaskTemplate.ContainerSpec.Privileges == nil {
		spec.TaskTemplate.ContainerSpec.Privileges = &dockerswarm.Privileges{}
	}
	return canonicalJSON(spec)
}

func canonicalJSON(value any) any {
	raw, _ := json.Marshal(value)
	var result any
	_ = json.Unmarshal(raw, &result)
	return result
}

func TestCLIEnvironmentDifferences(t *testing.T) {
	t.Setenv("AUDIT_ENV", "from-controller")
	for _, tc := range []struct {
		environment string
		native      string
	}{
		{"[AUDIT_ENV]", "from-controller"},
		{"{AUDIT_ENV: null}", "from-controller"},
		{"{AUDIT_ENV: ''}", ""},
	} {
		t.Run(tc.environment, func(t *testing.T) {
			source := "version: '3.8'\nservices: {api: {image: alpine, environment: " + tc.environment + "}}"
			var dictionary map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(source), &dictionary))
			cli, err := cliloader.Load(clitypes.ConfigDetails{WorkingDir: t.TempDir(), ConfigFiles: []clitypes.ConfigFile{{Config: dictionary}}, Environment: map[string]string{"AUDIT_ENV": "from-controller"}})
			require.NoError(t, err)
			require.NotNil(t, cli.Services[0].Environment["AUDIT_ENV"])
			assert.Equal(t, "from-controller", *cli.Services[0].Environment["AUDIT_ENV"])
			file, err := compose.NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) { return []byte(source), nil }).Load(t.Context(), "compose.yaml")
			require.NoError(t, err)
			assert.Equal(t, tc.native, file.Compose.Services[0].Environment.Map["AUDIT_ENV"])
		})
	}
}

func TestCLIFieldLimitations(t *testing.T) {
	var dictionary map[string]any
	require.NoError(t, yaml.Unmarshal([]byte("version: '3.8'\nservices: {api: {image: alpine, tmpfs: /run}}"), &dictionary))
	cli, err := cliloader.Load(clitypes.ConfigDetails{WorkingDir: t.TempDir(), ConfigFiles: []clitypes.ConfigFile{{Config: dictionary}}})
	require.NoError(t, err)
	spec, err := cliconvert.Service("1.51", cliconvert.NewNamespace("demo"), cli.Services[0], cli.Networks, cli.Volumes, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, spec.TaskTemplate.ContainerSpec.Mounts, "CLI loads service-level tmpfs but its converter omits it")
	require.NoError(t, yaml.Unmarshal([]byte("version: '3.8'\nservices: {api: {image: alpine, oom_score_adj: 100}}"), &dictionary))
	_, err = cliloader.Load(clitypes.ConfigDetails{WorkingDir: t.TempDir(), ConfigFiles: []clitypes.ConfigFile{{Config: dictionary}}})
	require.ErrorContains(t, err, "oom_score_adj")
}
