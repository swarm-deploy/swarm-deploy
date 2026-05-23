package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadInitJobsEnvironment(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    x-init-deploy-jobs:
      - name: migrate
        image: ghcr.io/acme/api:1.0.0
        command: ["./bin/migrate", "up"]
        environment:
          DB_HOST: postgres
          DB_USER: app
`)
	if err := os.WriteFile(composePath, composePayload, 0o600); err != nil {
		require.NoError(t, err, "write compose file")
	}

	file, err := Load(composePath)
	require.NoError(t, err, "load compose")

	require.Len(t, file.Services, 1, "expected 1 service")
	require.Len(t, file.Services["api"].InitJobs, 1, "expected 1 init job")

	first := file.Services["api"].InitJobs[0]
	assert.Equal(t, "postgres", first.Environment["DB_HOST"], "unexpected DB_HOST")
	assert.Equal(t, "app", first.Environment["DB_USER"], "unexpected DB_USER")
}

func TestLoadResolvesNetworkAliasesToNames(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  api:
    image: super-org/super-image:latest
    networks:
      - infra
    x-init-deploy-jobs:
      - name: migrations
        image: super-org/super-image:latest
      - name: explicit-network
        image: super-org/super-image:latest
        networks:
          - infra
networks:
  infra:
    name: wmb-infra
    external: true
`)
	if err := os.WriteFile(composePath, composePayload, 0o600); err != nil {
		require.NoError(t, err, "write compose file")
	}

	file, err := Load(composePath)
	require.NoError(t, err, "load compose")
	require.Len(t, file.Services, 1, "unexpected services count")

	service := file.Services["api"]
	require.Equal(t, []string{"wmb-infra"}, service.Networks, "service network alias must resolve to name")
	require.Len(t, service.InitJobs, 2, "unexpected init jobs count")
	assert.Nil(t, service.InitJobs[0].Networks, "job without explicit networks must keep nil networks")
	assert.Equal(
		t,
		[]string{"wmb-infra"},
		service.InitJobs[1].Networks,
		"job network alias must resolve to name",
	)
}
