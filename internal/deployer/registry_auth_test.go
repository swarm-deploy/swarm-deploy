package deployer

import (
	"encoding/base64"
	"testing"

	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
)

const privateImage = "wmb-prod.cr.cloud.ru/services/content-discovery-migrations:latest"

func TestBuildInitServiceCreateOptionsUsesAuthFromDockerAuthConfigEnv(t *testing.T) {
	rawAuth := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"wmb-prod.cr.cloud.ru":{"auth":"`+rawAuth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	runner := &InitJobRunner{
		authManager: registry.NewAuthManager(),
	}
	options, err := runner.buildInitServiceCreateOptions(privateImage)
	require.NoError(t, err, "build service create options")
	require.NotEmpty(t, options.EncodedRegistryAuth, "registry auth must be set for private registry")
	assert.True(t, options.QueryRegistry, "query registry must be enabled when registry auth is provided")

	authConfig, err := dockerregistry.DecodeAuthConfig(options.EncodedRegistryAuth)
	require.NoError(t, err, "decode encoded registry auth")
	assert.Equal(t, "robot", authConfig.Username, "unexpected auth username")
	assert.Equal(t, "secret", authConfig.Password, "unexpected auth password")
	assert.Equal(t, "wmb-prod.cr.cloud.ru", authConfig.ServerAddress, "unexpected registry host")
}

func TestBuildInitServiceCreateOptionsReturnsEmptyWithoutAuthConfig(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	runner := &InitJobRunner{
		authManager: registry.NewAuthManager(),
	}
	options, err := runner.buildInitServiceCreateOptions(privateImage)
	require.NoError(t, err, "build service create options")
	assert.Empty(t, options.EncodedRegistryAuth, "registry auth must be empty without auth config")
	assert.False(t, options.QueryRegistry, "query registry should be disabled without auth")
}
