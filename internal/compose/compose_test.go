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
	require.Len(t, file.Services[0].InitJobs, 1, "expected 1 init job")

	first := file.Services[0].InitJobs[0]
	assert.Equal(t, "postgres", first.Environment["DB_HOST"], "unexpected DB_HOST")
	assert.Equal(t, "app", first.Environment["DB_USER"], "unexpected DB_USER")
}

func TestLoadInitJobsIgnoresLegacyEnv(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    x-init-deploy-jobs:
      - name: legacy
        image: ghcr.io/acme/api:1.0.0
        env:
          LEGACY: "1"
`)
	if err := os.WriteFile(composePath, composePayload, 0o600); err != nil {
		require.NoError(t, err, "write compose file")
	}

	file, err := Load(composePath)
	require.NoError(t, err, "load compose")

	require.Len(t, file.Services, 1, "unexpected services count")
	require.Len(t, file.Services[0].InitJobs, 1, "unexpected init jobs count")
	assert.Nil(t, file.Services[0].InitJobs[0].Environment, "legacy env must be ignored")
}

func TestLoadResolvesNetworkAliasesToNames(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  content-discovery-grpc:
    image: wmb-prod.cr.cloud.ru/services/content-discovery-grpc:latest
    networks:
      - infra
    x-init-deploy-jobs:
      - name: migrations
        image: wmb-prod.cr.cloud.ru/services/content-discovery-migrations:latest
      - name: explicit-network
        image: wmb-prod.cr.cloud.ru/services/content-discovery-migrations:latest
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

	service := file.Services[0]
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

func TestLoadParsesServiceDeployLabels(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yaml")
	composePayload := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    deploy:
      labels:
        org.swarm-deploy.service.sync.policy.prune: "true"
`)
	require.NoError(t, os.WriteFile(composePath, composePayload, 0o600), "write compose file")

	file, err := Load(composePath)
	require.NoError(t, err, "load compose")
	require.Len(t, file.Services, 1, "expected one service")
	assert.Equal(
		t,
		"true",
		file.Services[0].DeployLabels["org.swarm-deploy.service.sync.policy.prune"],
		"unexpected deploy label",
	)
}

func TestApplyServiceDeployLabels(t *testing.T) {
	raw := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
  worker:
    image: ghcr.io/acme/worker:1.0.0
    deploy:
      labels:
        existing: value
`)

	file, err := Parse(raw)
	require.NoError(t, err, "parse compose")

	changed, err := file.ApplyServiceDeployLabels(map[string]string{
		"org.swarm-deploy.service.managed": "true",
	})
	require.NoError(t, err, "apply service labels")
	assert.True(t, changed, "expected compose mutation")

	servicesMap, ok := asMap(file.RawMap["services"])
	require.True(t, ok, "services map must exist")
	for name, rawService := range servicesMap {
		serviceMap, serviceMapOK := asMap(rawService)
		require.True(t, serviceMapOK, "service %s must be a map", name)
		deployMap, deployMapOK := asMap(serviceMap["deploy"])
		require.True(t, deployMapOK, "service %s deploy block must be a map", name)
		labelsMap, labelsMapOK := asMap(deployMap["labels"])
		require.True(t, labelsMapOK, "service %s deploy labels must be a map", name)
		assert.Equal(
			t,
			"true",
			asString(labelsMap["org.swarm-deploy.service.managed"]),
			"managed label must be set for service %s",
			name,
		)
		if name == "worker" {
			assert.Equal(t, "value", asString(labelsMap["existing"]), "existing labels must be preserved")
		}
	}
}

func TestApplyServiceDeployLabelsPreservesListLabelFormatValues(t *testing.T) {
	raw := []byte(`
services:
  api:
    image: ghcr.io/acme/api:1.0.0
    deploy:
      labels:
        - existing=value
`)

	file, err := Parse(raw)
	require.NoError(t, err, "parse compose")

	changed, err := file.ApplyServiceDeployLabels(map[string]string{
		"org.swarm-deploy.service.managed": "true",
	})
	require.NoError(t, err, "apply service labels")
	assert.True(t, changed, "expected compose mutation")

	servicesMap, ok := asMap(file.RawMap["services"])
	require.True(t, ok, "services map must exist")
	serviceMap, serviceMapOK := asMap(servicesMap["api"])
	require.True(t, serviceMapOK, "service api must be a map")
	deployMap, deployMapOK := asMap(serviceMap["deploy"])
	require.True(t, deployMapOK, "service api deploy block must be a map")
	labelsMap, labelsMapOK := asMap(deployMap["labels"])
	require.True(t, labelsMapOK, "service api deploy labels must be normalized as map")
	assert.Equal(t, "value", asString(labelsMap["existing"]), "existing list label must be preserved")
	assert.Equal(
		t,
		"true",
		asString(labelsMap["org.swarm-deploy.service.managed"]),
		"managed label must be added",
	)
}
