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

func TestDockerSocketMountAnalyzerAnalyze(t *testing.T) {
	now := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		volume    string
		wantCount int
	}{
		{
			name:      "short syntax var run socket",
			volume:    `- /var/run/docker.sock:/var/run/docker.sock`,
			wantCount: 1,
		},
		{
			name:      "short syntax run socket read only",
			volume:    `- /run/docker.sock:/docker.sock:ro`,
			wantCount: 1,
		},
		{
			name: "long syntax bind socket",
			volume: `- type: bind
        source: /var/run/docker.sock
        target: /var/run/docker.sock`,
			wantCount: 1,
		},
		{
			name:   "named volume",
			volume: `- docker-data:/var/lib/docker`,
		},
		{
			name:   "unrelated bind mount",
			volume: `- /var/log:/var/log`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			definition, err := compose.Parse([]byte("services:\n  agent:\n    image: agent:1\n    volumes:\n      " + testCase.volume + "\n"))
			require.NoError(t, err)
			analyzer := &DockerSocketMountAnalyzer{now: func() time.Time { return now }}
			stack := recommendationTestStack(definition.Services...)

			recommendations := analyzer.Analyze(context.Background(), stack)

			require.Len(t, recommendations, testCase.wantCount, "unexpected recommendation count")
			if testCase.wantCount == 0 {
				return
			}

			recommendation := recommendations[0]
			assert.Equal(t, model.TypeServiceDockerSocketMount, recommendation.Type)
			assert.Equal(t, model.SeverityHigh, recommendation.Severity)
			assert.Equal(t, model.Subject{Stack: "payments", Service: "agent"}, recommendation.Subject)
			assert.Equal(t, model.Source{File: "compose.yaml", Digest: "compose-digest", Commit: "abc123"}, recommendation.Source)
			assert.Equal(t, now, recommendation.CreatedAt)
			assert.Contains(t, recommendation.Recommendation, "broad access to the Docker daemon")
		})
	}
}
