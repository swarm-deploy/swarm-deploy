package registry

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const privateImage = "wmb-prod.cr.cloud.ru/services/content-discovery-migrations:latest"

func TestAuthManagerResolveImageUsesAuthFromDockerAuthConfigEnv(t *testing.T) {
	rawAuth := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"wmb-prod.cr.cloud.ru":{"auth":"`+rawAuth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	manager := NewAuthManager()
	encodedAuth, err := manager.ResolveImage(privateImage)
	require.NoError(t, err, "resolve image auth")
	require.NotEmpty(t, encodedAuth, "registry auth must be set for private registry")

	authConfig, err := dockerregistry.DecodeAuthConfig(encodedAuth)
	require.NoError(t, err, "decode encoded registry auth")
	assert.Equal(t, "robot", authConfig.Username, "unexpected auth username")
	assert.Equal(t, "secret", authConfig.Password, "unexpected auth password")
	assert.Equal(t, "wmb-prod.cr.cloud.ru", authConfig.ServerAddress, "unexpected registry host")
}

func TestAuthManagerResolveImageReturnsEmptyWithoutAuthConfig(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	manager := NewAuthManager()
	encodedAuth, err := manager.ResolveImage(privateImage)
	require.NoError(t, err, "resolve image auth")
	assert.Empty(t, encodedAuth, "registry auth must be empty without auth config")
}

func TestAuthManagerResolveImageKeepsConstructorEnvConfig(t *testing.T) {
	firstRawAuth := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"wmb-prod.cr.cloud.ru":{"auth":"`+firstRawAuth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	manager := NewAuthManager()

	secondRawAuth := base64.StdEncoding.EncodeToString([]byte("robot-updated:secret-updated"))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"wmb-prod.cr.cloud.ru":{"auth":"`+secondRawAuth+`"}}}`)

	encodedAuth, err := manager.ResolveImage(privateImage)
	require.NoError(t, err, "resolve image auth")
	require.NotEmpty(t, encodedAuth, "registry auth must be set for private registry")

	authConfig, err := dockerregistry.DecodeAuthConfig(encodedAuth)
	require.NoError(t, err, "decode encoded registry auth")
	assert.Equal(t, "robot", authConfig.Username, "constructor config should be used")
	assert.Equal(t, "secret", authConfig.Password, "constructor config should be used")
}

func TestAuthManagerResolveImageReturnsErrorOnInvalidConfig(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	manager := NewAuthManager()
	encodedAuth, err := manager.ResolveImage(privateImage)
	require.Error(t, err)
	assert.ErrorContains(t, err, "decode docker auth config")
	assert.Empty(t, encodedAuth)
}

func TestAuthManagerInvalidConstructorEnvDoesNotFallBackToFile(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":`)
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	manager := NewAuthManager()
	t.Setenv("DOCKER_AUTH_CONFIG", "")

	_, err := manager.ResolveImage(privateImage)
	require.ErrorContains(t, err, "decode docker auth config")
}

func TestAuthManagerResolveImageReloadsDockerConfigFile(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	configDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", configDir)
	configPath := filepath.Join(configDir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":{"wmb-prod.cr.cloud.ru":{"username":"first","password":"old"}}}`), 0o600))

	manager := NewAuthManager()
	first, err := manager.ResolveImage(privateImage)
	require.NoError(t, err)
	firstAuth, err := dockerregistry.DecodeAuthConfig(first)
	require.NoError(t, err)
	assert.Equal(t, "first", firstAuth.Username)

	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":{"wmb-prod.cr.cloud.ru":{"username":"second","password":"new"}}}`), 0o600))
	second, err := manager.ResolveImage(privateImage)
	require.NoError(t, err)
	secondAuth, err := dockerregistry.DecodeAuthConfig(second)
	require.NoError(t, err)
	assert.Equal(t, "second", secondAuth.Username)
	assert.Equal(t, "new", secondAuth.Password)
}

func TestAuthManagerResolveImageRecoversAfterConfigRepair(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	configDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", configDir)
	configPath := filepath.Join(configDir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":`), 0o600))

	manager := NewAuthManager()
	_, err := manager.ResolveImage(privateImage)
	require.ErrorContains(t, err, "decode docker auth config")

	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":{"wmb-prod.cr.cloud.ru":{"username":"repaired","password":"secret"}}}`), 0o600))
	encoded, err := manager.ResolveImage(privateImage)
	require.NoError(t, err)
	auth, err := dockerregistry.DecodeAuthConfig(encoded)
	require.NoError(t, err)
	assert.Equal(t, "repaired", auth.Username)
}

func TestAuthManagerResolveImageRejectsUnreadableConfigPath(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	configDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", configDir)
	require.NoError(t, os.Mkdir(filepath.Join(configDir, "config.json"), 0o700))

	_, err := NewAuthManager().ResolveImage(privateImage)
	require.ErrorContains(t, err, "read docker auth config")
}

func TestAuthManagerResolveImageReloadsAtomicReplacementWithSameMetadata(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	configDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", configDir)
	configPath := filepath.Join(configDir, "config.json")
	first := []byte(`{"auths":{"wmb-prod.cr.cloud.ru":{"username":"first","password":"secret"}}}`)
	second := []byte(`{"auths":{"wmb-prod.cr.cloud.ru":{"username":"other","password":"secret"}}}`)
	require.Equal(t, len(first), len(second))
	require.NoError(t, os.WriteFile(configPath, first, 0o600))
	stamp := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(configPath, stamp, stamp))

	manager := NewAuthManager()
	encoded, err := manager.ResolveImage(privateImage)
	require.NoError(t, err)
	auth, err := dockerregistry.DecodeAuthConfig(encoded)
	require.NoError(t, err)
	assert.Equal(t, "first", auth.Username)

	replacement := filepath.Join(configDir, "replacement.json")
	require.NoError(t, os.WriteFile(replacement, second, 0o600))
	require.NoError(t, os.Chtimes(replacement, stamp, stamp))
	require.NoError(t, os.Rename(replacement, configPath))

	encoded, err = manager.ResolveImage(privateImage)
	require.NoError(t, err)
	auth, err = dockerregistry.DecodeAuthConfig(encoded)
	require.NoError(t, err)
	assert.Equal(t, "other", auth.Username)
}

func TestAuthManagerResolveImageDoesNotReuseStaleCredentialsAfterInvalidUpdate(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	configDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", configDir)
	configPath := filepath.Join(configDir, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":{"wmb-prod.cr.cloud.ru":{"username":"first","password":"secret"}}}`), 0o600))

	manager := NewAuthManager()
	_, err := manager.ResolveImage(privateImage)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, []byte(`{"auths":`), 0o600))
	encoded, err := manager.ResolveImage(privateImage)
	require.ErrorContains(t, err, "decode docker auth config")
	assert.Empty(t, encoded)
}

func TestAuthManagerResolveImageConcurrentLookups(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"wmb-prod.cr.cloud.ru":{"username":"robot","password":"secret"}}}`)
	manager := NewAuthManager()
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				encoded, err := manager.ResolveImage(privateImage)
				assert.NoError(t, err)
				if err != nil {
					return
				}
				auth, err := dockerregistry.DecodeAuthConfig(encoded)
				assert.NoError(t, err)
				assert.Equal(t, "robot", auth.Username)
			}
		}()
	}
	wg.Wait()
}
