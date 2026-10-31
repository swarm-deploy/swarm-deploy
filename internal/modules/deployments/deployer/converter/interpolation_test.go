package converter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"go.uber.org/mock/gomock"
)

func TestConvertInterpolatedSource(t *testing.T) {
	t.Setenv("SD_TEST_TAG", "alpine")
	t.Setenv("SD_TEST_VALUE", "hello world")
	t.Setenv("SD_TEST_EMPTY", "")
	t.Setenv("SD_TEST_LITERAL", "$HOME $$HOME ${SD_TEST_TAG}")
	for _, tc := range []struct{ name, command, entrypoint string }{
		{"list", `["sh", "-c", "echo $SD_TEST_VALUE $$HOME $$(date) $$$$"]`, `["/bin/${SD_TEST_SHELL:-sh}", "-c"]`},
		{"scalar", `'sh -c "echo $SD_TEST_VALUE $$HOME $$(date) $$$$"'`, `'/bin/${SD_TEST_SHELL:-sh} -c'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`services:
  app:
    image: nginx:${SD_TEST_TAG}
    command: %s
    entrypoint: %s
    environment:
      ESCAPED: '$$HOME'
      PID: '$$$$'
      INSERTED: '${SD_TEST_LITERAL}'
      SOFT_DEFAULT: '${SD_TEST_EMPTY:-fallback}'
      HARD_DEFAULT: '${SD_TEST_EMPTY-fallback}'
      MISSING_DEFAULT: '${SD_TEST_UNSET_217-fallback}'
      MISSING: '$SD_TEST_UNSET_217'
      REQUIRED: '${SD_TEST_TAG:?required}'
      REQUIRED_SET: '${SD_TEST_EMPTY?required}'
      NESTED_DEFAULT: '${SD_TEST_UNSET_217:-$SD_TEST_TAG}'
    healthcheck:
      test: 'echo $SD_TEST_VALUE $$HOME $$(date)'
      interval: '${SD_TEST_INTERVAL:-2s}'
      retries: '${SD_TEST_RETRIES:-3}'
    deploy: {replicas: '${SD_TEST_REPLICAS:-01}'}
    networks: [default]
    volumes: ['data:/data:ro']
networks:
  default:
    name: '${SD_TEST_NETWORK:-shared}'
    attachable: '${SD_TEST_ATTACHABLE:-yes}'
    driver_opts: {encrypted: '${SD_TEST_EMPTY}'}
volumes:
  data:
    name: '${SD_TEST_VOLUME:-storage}'
    driver: '${SD_TEST_DRIVER:-local}'
    driver_opts: {type: '${SD_TEST_TYPE:-tmpfs}', device: '$$HOME'}
`, tc.command, tc.entrypoint)
			loader := compose.NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
				return []byte(source), nil
			})
			file, err := loader.Load(t.Context(), "compose.yaml")
			require.NoError(t, err)
			converter, networks, _, _ := newTestConverter(t)
			networks.EXPECT().Get(gomock.Any(), "shared").Return(existingNetwork("shared"), nil)
			result, err := converter.Convert(t.Context(), "demo", file.Compose)
			require.NoError(t, err)
			spec := result.Services[0]
			container := spec.TaskTemplate.ContainerSpec
			assert.Equal(t, "nginx:alpine", container.Image)
			assert.Equal(t, []string{"sh", "-c", "echo hello world $HOME $(date) $$"}, container.Args)
			assert.Equal(t, []string{"/bin/sh", "-c"}, container.Command)
			assert.ElementsMatch(t, []string{
				"ESCAPED=$HOME", "PID=$$", "INSERTED=$HOME $$HOME ${SD_TEST_TAG}",
				"SOFT_DEFAULT=fallback", "HARD_DEFAULT=", "MISSING_DEFAULT=fallback", "MISSING=",
				"REQUIRED=alpine", "REQUIRED_SET=", "NESTED_DEFAULT=$SD_TEST_TAG",
			}, container.Env)
			assert.Equal(t, []string{"CMD-SHELL", "echo hello world $HOME $(date)"}, container.Healthcheck.Test)
			assert.Equal(t, 2*time.Second, container.Healthcheck.Interval)
			assert.Equal(t, 3, container.Healthcheck.Retries)
			assert.Equal(t, uint64(1), *spec.Mode.Replicated.Replicas)
			assert.Equal(t, "storage", container.Mounts[0].Source)
			assert.Equal(t, "$HOME", container.Mounts[0].VolumeOptions.DriverConfig.Options["device"])
			assert.True(t, result.Networks[0].Spec.Attachable)
		})
	}
}

func TestConvertInterpolatedHealthcheckList(t *testing.T) {
	t.Setenv("SD_TEST_VALUE", "ok")
	converter, networks, _, _ := newTestConverter(t)
	networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
	desired := parseStack(t, `services: {app: {image: alpine, healthcheck: {test: [CMD-SHELL, 'echo ${SD_TEST_VALUE} $$HOME $$$$']}}}`)
	result, err := converter.Convert(t.Context(), "demo", desired)
	require.NoError(t, err)
	assert.Equal(t, []string{"CMD-SHELL", "echo ok $HOME $$"}, result.Services[0].TaskTemplate.ContainerSpec.Healthcheck.Test)
}
