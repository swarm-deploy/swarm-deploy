package deployer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestBuildInitServiceSpecMapsEntrypointAndCommand(t *testing.T) {
	runner := &InitJobRunner{}

	serviceSpec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:   "test-stack",
		ServiceName: "api",
		Job: compose.InitJob{
			Name:       "migrate",
			Image:      "ghcr.io/acme/api:1.0.0",
			Entrypoint: []string{"/bin/sh", "-c"},
			Command:    []string{"./bin/migrate up"},
		},
	}, "migrate-test")
	require.NoError(t, err)
	require.NotNil(t, serviceSpec.TaskTemplate.ContainerSpec)

	assert.Equal(t, []string{"/bin/sh", "-c"}, serviceSpec.TaskTemplate.ContainerSpec.Command)
	assert.Equal(t, []string{"./bin/migrate up"}, serviceSpec.TaskTemplate.ContainerSpec.Args)
}

func TestBuildInitServiceSpecMapsCommandToArgsWithoutEntrypoint(t *testing.T) {
	runner := &InitJobRunner{}

	serviceSpec, err := runner.buildInitServiceSpec(context.Background(), InitJobSpec{
		StackName:   "test-stack",
		ServiceName: "api",
		Job: compose.InitJob{
			Name:    "migrate",
			Image:   "ghcr.io/acme/api:1.0.0",
			Command: []string{"./bin/migrate", "up"},
		},
	}, "migrate-test")
	require.NoError(t, err)
	require.NotNil(t, serviceSpec.TaskTemplate.ContainerSpec)

	assert.Empty(t, serviceSpec.TaskTemplate.ContainerSpec.Command)
	assert.Equal(t, []string{"./bin/migrate", "up"}, serviceSpec.TaskTemplate.ContainerSpec.Args)
}
