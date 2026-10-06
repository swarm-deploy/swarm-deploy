package analyzer

import (
	"context"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

func TestHostPortStartFirstAnalyzerAnalyze(t *testing.T) {
	now := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		service   compose.Service
		wantCount int
	}{
		{
			name: "host port with start-first",
			service: compose.Service{
				Name: "api",
				Ports: compose.ServicePorts{Ports: []compose.ServicePort{
					{Published: 8080, Target: 80, Mode: dockerswarm.PortConfigPublishModeHost},
				}},
				Deploy: compose.ServiceDeploy{
					UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: "start-first"},
				},
			},
			wantCount: 1,
		},
		{
			name: "host port with normalized start-first",
			service: compose.Service{
				Name: "api",
				Ports: compose.ServicePorts{Ports: []compose.ServicePort{
					{Published: 8080, Target: 80, Mode: dockerswarm.PortConfigPublishModeHost},
				}},
				Deploy: compose.ServiceDeploy{
					UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: " START-FIRST "},
				},
			},
			wantCount: 1,
		},
		{
			name: "ingress port with start-first",
			service: compose.Service{
				Name: "api",
				Ports: compose.ServicePorts{Ports: []compose.ServicePort{
					{Published: 8080, Target: 80, Mode: dockerswarm.PortConfigPublishModeIngress},
				}},
				Deploy: compose.ServiceDeploy{
					UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: "start-first"},
				},
			},
		},
		{
			name: "host port with stop-first",
			service: compose.Service{
				Name: "api",
				Ports: compose.ServicePorts{Ports: []compose.ServicePort{
					{Published: 8080, Target: 80, Mode: dockerswarm.PortConfigPublishModeHost},
				}},
				Deploy: compose.ServiceDeploy{
					UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: "stop-first"},
				},
			},
		},
		{
			name: "host port without update config",
			service: compose.Service{
				Name: "api",
				Ports: compose.ServicePorts{Ports: []compose.ServicePort{
					{Published: 8080, Target: 80, Mode: dockerswarm.PortConfigPublishModeHost},
				}},
			},
		},
		{
			name: "multiple host ports produce one recommendation",
			service: compose.Service{
				Name: "api",
				Ports: compose.ServicePorts{Ports: []compose.ServicePort{
					{Published: 8080, Target: 80, Mode: dockerswarm.PortConfigPublishModeHost},
					{Published: 8443, Target: 443, Mode: dockerswarm.PortConfigPublishModeHost},
				}},
				Deploy: compose.ServiceDeploy{
					UpdateConfig: &compose.ServiceDeployUpdateConfig{Order: "start-first"},
				},
			},
			wantCount: 1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := &HostPortStartFirstAnalyzer{now: func() time.Time { return now }}
			stack := recommendationTestStack(testCase.service)

			recommendations := analyzer.Analyze(context.Background(), stack)

			require.Len(t, recommendations, testCase.wantCount)
			if testCase.wantCount == 0 {
				return
			}

			recommendation := recommendations[0]
			assert.Equal(t, model.TypeServiceHostPortStartFirst, recommendation.Type)
			assert.Equal(t, model.SeverityMedium, recommendation.Severity)
			assert.Equal(t, model.Subject{Stack: "payments", Service: "api"}, recommendation.Subject)
			assert.Equal(t, model.Source{File: "compose.yaml", Digest: "compose-digest", Commit: "abc123"}, recommendation.Source)
			assert.Equal(t, now, recommendation.CreatedAt)
			assert.Contains(t, recommendation.Recommendation, "stop-first")
			assert.Contains(t, recommendation.Recommendation, "ingress")
		})
	}
}
