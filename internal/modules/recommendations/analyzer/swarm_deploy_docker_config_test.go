package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

func TestSwarmDeployDockerConfigAnalyzerAnalyze(t *testing.T) {
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		compose   string
		wantCount int
	}{
		{
			name: "missing docker config",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
`,
			wantCount: 1,
		},
		{
			name: "unrelated service",
			compose: `services:
  api:
    image: example/api:1.0.0
`,
		},
		{
			name: "inline docker auth config",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
    environment:
      DOCKER_AUTH_CONFIG: '{"auths":{"registry.example.com":{"auth":"token"}}}'
`,
		},
		{
			name: "docker secret in configured directory",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
    environment:
      DOCKER_CONFIG: /run/secrets
    secrets:
      - source: registry_auth_config
        target: config.json
secrets:
  registry_auth_config:
    file: ./docker-config.json
`,
		},
		{
			name: "short secret reference",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
    environment:
      DOCKER_CONFIG: /run/secrets
    secrets:
      - config.json
secrets:
  config.json:
    file: ./docker-config.json
`,
		},
		{
			name: "default docker config bind mount",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
    volumes:
      - ./docker-config.json:/root/.docker/config.json:ro
`,
		},
		{
			name: "configured docker config directory mount",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
    environment:
      DOCKER_CONFIG: /docker
    volumes:
      - ./.docker:/docker:ro
`,
		},
		{
			name: "docker secret at wrong path",
			compose: `services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:1.0.0
    environment:
      DOCKER_CONFIG: /docker
    secrets:
      - source: registry_auth_config
        target: config.json
secrets:
  registry_auth_config:
    file: ./docker-config.json
`,
			wantCount: 1,
		},
		{
			name: "local image",
			compose: `services:
  controller:
    image: swarm-deploy:local
`,
			wantCount: 1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			definition, err := compose.Parse([]byte(testCase.compose))
			require.NoError(t, err)

			analyzer := &SwarmDeployDockerConfigAnalyzer{now: func() time.Time { return now }}
			stack := model.Stack{
				Name: "platform",
				Definition: compose.File{
					Path:    "compose.yaml",
					Digest:  "compose-digest",
					Compose: definition,
				},
				Commit: "abc123",
			}

			recommendations := analyzer.Analyze(context.Background(), stack)

			require.Len(t, recommendations, testCase.wantCount)
			if testCase.wantCount == 0 {
				return
			}

			recommendation := recommendations[0]
			assert.Equal(t, model.TypeServiceSwarmDeployDockerConfigMissing, recommendation.Type)
			assert.Equal(t, model.SeverityMedium, recommendation.Severity)
			assert.Equal(t, "platform", recommendation.Subject.Stack)
			assert.Equal(t, definition.Services[0].Name, recommendation.Subject.Service)
			assert.Equal(t, model.Source{File: "compose.yaml", Digest: "compose-digest", Commit: "abc123"}, recommendation.Source)
			assert.Equal(t, now, recommendation.CreatedAt)
			assert.Contains(t, recommendation.Recommendation, "DOCKER_CONFIG=/run/secrets")
		})
	}
}
