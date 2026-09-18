package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
)

func TestRestartPolicyUnspecifiedAnalyzerAnalyze(t *testing.T) {
	now := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		deploy    compose.ServiceDeploy
		wantCount int
	}{
		{
			name:      "regular service without restart policy",
			wantCount: 1,
		},
		{
			name: "regular service with restart policy",
			deploy: compose.ServiceDeploy{
				RestartPolicy: &compose.ServiceDeployRestartPolicy{Condition: "on-failure"},
			},
		},
		{
			name: "swarm cron job",
			deploy: compose.ServiceDeploy{
				Labels: *compose.NewLabels(map[string]string{swarmCronjobEnableLabel: "true"}),
			},
		},
		{
			name: "disabled swarm cron job",
			deploy: compose.ServiceDeploy{
				Labels: *compose.NewLabels(map[string]string{swarmCronjobEnableLabel: "false"}),
			},
			wantCount: 1,
		},
		{
			name:   "native replicated job",
			deploy: compose.ServiceDeploy{Mode: "replicated-job"},
		},
		{
			name:   "native global job",
			deploy: compose.ServiceDeploy{Mode: "global-job"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := &RestartPolicyUnspecifiedAnalyzer{now: func() time.Time { return now }}
			stack := recommendationTestStack(compose.Service{Name: "worker", Deploy: testCase.deploy})

			recommendations := analyzer.Analyze(context.Background(), stack)

			require.Len(t, recommendations, testCase.wantCount, "unexpected recommendation count")
			if testCase.wantCount == 0 {
				return
			}

			recommendation := recommendations[0]
			assert.Equal(t, model.TypeServiceRestartPolicyUnspecified, recommendation.Type)
			assert.Equal(t, model.SeverityLow, recommendation.Severity)
			assert.Equal(t, model.Subject{Stack: "payments", Service: "worker"}, recommendation.Subject)
			assert.Equal(t, model.Source{File: "compose.yaml", Digest: "compose-digest", Commit: "abc123"}, recommendation.Source)
			assert.Equal(t, now, recommendation.CreatedAt)
			assert.Contains(t, recommendation.Recommendation, "deploy.restart_policy")
		})
	}
}

func recommendationTestStack(services ...compose.Service) model.Stack {
	return model.Stack{
		Name: "payments",
		Definition: compose.File{
			Path:   "compose.yaml",
			Digest: "compose-digest",
			Compose: compose.Compose{
				Services: services,
			},
		},
		Commit: "abc123",
	}
}
