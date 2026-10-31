package compose

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterpolationErrorsDoNotExposeValues(t *testing.T) {
	t.Setenv("SD_TEST_EMPTY", "")
	t.Setenv("SD_TEST_PRIVATE", "private-interpolation-value")
	for _, expression := range []string{
		"${SD_TEST_UNSET_217:?private_error_message}", "${SD_TEST_EMPTY:?private_error_message}",
		"${SD_TEST_UNSET_217?private_error_message}", "$SD_TEST_PRIVATE ${", "${SD_TEST_PRIVATE:+unsupported}",
	} {
		t.Run(expression, func(t *testing.T) {
			source := []byte(fmt.Sprintf("services: {app: {image: alpine, environment: {VALUE: '%s'}}}", expression))
			loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) { return source, nil })
			_, err := loader.Load(t.Context(), "compose.yaml")
			var validation *ValidateComposeError
			require.ErrorAs(t, err, &validation)
			assert.NotContains(t, err.Error(), "private_error_message")
			assert.NotContains(t, err.Error(), "private-interpolation-value")
		})
	}
}

func TestInterpolationPreservesPayloadsEnvPrecedenceAndSourceDigest(t *testing.T) {
	t.Setenv("SD_TEST_TAG", "alpine")
	t.Setenv("SD_TEST_ENV", "first.env")
	files := map[string]string{
		"compose.yaml": `services:
  app:
    image: nginx:${SD_TEST_TAG}
    env_file: ['${SD_TEST_ENV}', second.env]
    environment: {PRIORITY: '${SD_TEST_TAG}', EMPTY: '', ESCAPED: '$$HOME'}
configs: {settings: {file: settings.txt}}
secrets: {password: {file: password.txt}}
`,
		"first.env":    "PRIORITY=first\nFILE_ORDER=first\nEMPTY=first\nLITERAL=$SD_TEST_TAG $$HOME\n",
		"second.env":   "PRIORITY=second\nFILE_ORDER=second\n",
		"settings.txt": "${SD_TEST_TAG} $$HOME",
		"password.txt": "${SD_TEST_TAG:?must stay literal} $$HOME",
	}
	var secretReads []string
	loader := NewFileLoaderWithReader(func(_ context.Context, path string) ([]byte, error) {
		value, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("unexpected file")
		}
		if path == "password.txt" {
			secretReads = append(secretReads, value)
		}
		return []byte(value), nil
	})
	first, err := loader.Load(t.Context(), "compose.yaml")
	require.NoError(t, err)
	NewEnvFilePopulator().Populate(first)
	assert.Equal(t, map[string]string{
		"PRIORITY": "alpine", "FILE_ORDER": "second", "EMPTY": "", "ESCAPED": "$HOME", "LITERAL": "$SD_TEST_TAG $$HOME",
	}, first.Compose.Services[0].Environment.Map)
	assert.Empty(t, first.Compose.Services[0].EnvFiles)
	assert.Equal(t, []byte(files["settings.txt"]), first.Compose.Configs["settings"].Data)
	assert.Equal(t, []string{files["password.txt"]}, secretReads)
	t.Setenv("SD_TEST_TAG", "stable")
	second, err := loader.Load(t.Context(), "compose.yaml")
	require.NoError(t, err)
	assert.Equal(t, "nginx:stable", second.Compose.Services[0].Image)
	assert.Equal(t, first.Digest, second.Digest, "source digest must not hash the process environment")
	assert.Equal(t, first.ResourceDigests, second.ResourceDigests)
}

func TestInterpolationPreservesServiceOrderAndAliases(t *testing.T) {
	t.Setenv("SD_TEST_TAG", "alpine")
	loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
		return []byte(`x-common: &common
  image: 'nginx:${SD_TEST_TAG}'
  environment: {DOLLAR: '$$$$'}
services:
  z:
    <<: *common
    command: ['echo', '$$(date)']
    x-init-deploy-jobs:
      - name: init
        image: alpine:3
        command: ['echo $(date) $HOME $$HOME ${SD_TEST_TAG}']
  a:
    <<: *common
`), nil
	})
	file, err := loader.Load(t.Context(), "compose.yaml")
	require.NoError(t, err)
	schema := file.Compose
	require.Len(t, schema.Services, 2)
	assert.Equal(t, "z", schema.Services[0].Name)
	assert.Equal(t, "a", schema.Services[1].Name)
	for _, service := range schema.Services {
		assert.Equal(t, "nginx:alpine", service.Image)
		assert.Equal(t, "$$", service.Environment.Map["DOLLAR"])
	}
	assert.Equal(t, []string{"echo", "$(date)"}, schema.Services[0].Command.Args)
	assert.Equal(t, []string{"echo $(date) $HOME $$HOME ${SD_TEST_TAG}"}, schema.Services[0].InitJobs[0].Command)
}

func TestParseKeepsRawSourceForChangeComparison(t *testing.T) {
	t.Setenv("SD_TEST_TAG", "alpine")
	schema, err := Parse([]byte(`services: {app: {image: 'nginx:${SD_TEST_TAG}', environment: {VALUE: '$$$$'}}}`))
	require.NoError(t, err)
	assert.Equal(t, "nginx:${SD_TEST_TAG}", schema.Services[0].Image)
	assert.Equal(t, "$$$$", schema.Services[0].Environment.Map["VALUE"])
}

func TestEffectiveValidationDoesNotEchoInterpolatedValues(t *testing.T) {
	t.Setenv("SD_TEST_PRIVATE", "private-interpolation-value")
	for _, source := range []string{
		`services: {app: {deploy: {replicas: '${SD_TEST_PRIVATE}'}}}`,
		`services: {app: {deploy: {labels: {org.swarm-deploy.service.sync.policy.selfHeal: '${SD_TEST_PRIVATE}'}}}}`,
		`services: {app: {env_file: '${SD_TEST_PRIVATE}'}}`,
		`configs: {settings: {file: '${SD_TEST_PRIVATE}'}}`,
		`secrets: {password: {file: '${SD_TEST_PRIVATE}'}}`,
	} {
		t.Run(source, func(t *testing.T) {
			loader := NewFileLoaderWithReader(func(_ context.Context, path string) ([]byte, error) {
				if path == "compose.yaml" {
					return []byte(source), nil
				}
				return nil, fmt.Errorf("cannot read %s", path)
			})
			_, err := loader.Load(t.Context(), "compose.yaml")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-interpolation-value")
		})
	}
}
