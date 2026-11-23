package analyzer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

func TestImageAnalyzerAnalyze(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	now := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		image        string
		wantType     model.Type
		wantSeverity model.Severity
		wantCount    int
	}{
		{
			name:      "image without explicit tag",
			image:     "nginx",
			wantCount: 0,
		},
		{
			name:         "latest tag",
			image:        "nginx:latest",
			wantType:     model.TypeServiceImageLatest,
			wantSeverity: model.SeverityHigh,
			wantCount:    1,
		},
		{
			name:         "version tag",
			image:        "nginx:1.29",
			wantType:     model.TypeServiceImageDigestUnspecified,
			wantSeverity: model.SeverityMedium,
			wantCount:    1,
		},
		{
			name:      "digest pinned without tag",
			image:     "nginx@" + digest,
			wantCount: 0,
		},
		{
			name:      "digest pinned with tag",
			image:     "nginx:1.29@" + digest,
			wantCount: 0,
		},
		{
			name:         "registry latest tag",
			image:        "ghcr.io/example/api:latest",
			wantType:     model.TypeServiceImageLatest,
			wantSeverity: model.SeverityHigh,
			wantCount:    1,
		},
		{
			name:         "registry version tag",
			image:        "ghcr.io/example/api:v1.2.3",
			wantType:     model.TypeServiceImageDigestUnspecified,
			wantSeverity: model.SeverityMedium,
			wantCount:    1,
		},
		{
			name:         "registry port with version tag",
			image:        "registry.example.com:5000/api:v1",
			wantType:     model.TypeServiceImageDigestUnspecified,
			wantSeverity: model.SeverityMedium,
			wantCount:    1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := &ImageAnalyzer{now: func() time.Time { return now }}
			stack := model.Stack{
				Name: "payments",
				Definition: compose.File{
					Path:   "compose.yaml",
					Digest: "compose-digest",
					Compose: compose.Compose{
						Services: compose.Services{
							{
								Name:  "api",
								Image: testCase.image,
							},
						},
					},
				},
				Commit: "abc123",
			}

			recommendations := analyzer.Analyze(context.Background(), stack)

			require.Len(t, recommendations, testCase.wantCount, "unexpected recommendation count")
			if testCase.wantCount == 0 {
				return
			}

			rec := recommendations[0]
			assert.Equal(t, testCase.wantType, rec.Type, "unexpected recommendation type")
			assert.Equal(t, testCase.wantSeverity, rec.Severity, "unexpected recommendation severity")
			assert.Equal(t, "payments", rec.Subject.Stack, "unexpected subject stack")
			assert.Equal(t, "api", rec.Subject.Service, "unexpected subject service")
			assert.Equal(t, model.Source{File: "compose.yaml", Digest: "compose-digest", Commit: "abc123"}, rec.Source, "unexpected source")
			assert.Equal(t, now, rec.CreatedAt, "unexpected created at")
		})
	}
}

func TestImageAnalyzerAnalyzeMultipleServices(t *testing.T) {
	analyzer := &ImageAnalyzer{now: time.Now}
	stack := model.Stack{
		Name: "payments",
		Definition: compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{
						Name:  "api",
						Image: "ghcr.io/example/api:latest",
					},
					{
						Name:  "worker",
						Image: "registry.example.com:5000/worker:v1",
					},
					{
						Name:  "web",
						Image: "nginx",
					},
				},
			},
		},
	}

	recommendations := analyzer.Analyze(context.Background(), stack)

	require.Len(t, recommendations, 2, "unexpected recommendation count")
	assert.Equal(t, model.TypeServiceImageLatest, recommendations[0].Type, "unexpected api type")
	assert.Equal(t, model.SeverityHigh, recommendations[0].Severity, "unexpected api severity")
	assert.Equal(t, model.Subject{Stack: "payments", Service: "api"}, recommendations[0].Subject, "unexpected api subject")
	assert.Equal(t, model.TypeServiceImageDigestUnspecified, recommendations[1].Type, "unexpected worker type")
	assert.Equal(t, model.SeverityMedium, recommendations[1].Severity, "unexpected worker severity")
	assert.Equal(t, model.Subject{Stack: "payments", Service: "worker"}, recommendations[1].Subject, "unexpected worker subject")
}
