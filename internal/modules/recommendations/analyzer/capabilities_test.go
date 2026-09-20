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

func TestServiceCapabilitiesAnalyzerAnalyze(t *testing.T) {
	now := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		capAdd         string
		capDrop        string
		wantTypes      []model.Type
		wantSeverities []model.Severity
	}{
		{name: "no added capabilities"},
		{
			name:           "all capabilities",
			capAdd:         "[ALL]",
			wantTypes:      []model.Type{model.TypeServiceCapabilitiesAll},
			wantSeverities: []model.Severity{model.SeverityHigh},
		},
		{
			name:           "high risk capabilities",
			capAdd:         "[SYS_ADMIN, SYS_MODULE]",
			wantTypes:      []model.Type{model.TypeServiceCapabilitiesPrivileged},
			wantSeverities: []model.Severity{model.SeverityHigh},
		},
		{
			name:           "medium risk capabilities",
			capAdd:         "[NET_ADMIN, SYS_PTRACE, NET_RAW]",
			wantTypes:      []model.Type{model.TypeServiceCapabilitiesSensitive},
			wantSeverities: []model.Severity{model.SeverityMedium},
		},
		{
			name:    "ordinary narrow capability",
			capAdd:  "[NET_BIND_SERVICE]",
			capDrop: "[ALL]",
		},
		{
			name:           "mixed high and medium risk capabilities",
			capAdd:         "[sys_admin, net_raw]",
			wantTypes:      []model.Type{model.TypeServiceCapabilitiesPrivileged, model.TypeServiceCapabilitiesSensitive},
			wantSeverities: []model.Severity{model.SeverityHigh, model.SeverityMedium},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			raw := "services:\n  api:\n    image: api:1\n"
			if testCase.capAdd != "" {
				raw += "    cap_add: " + testCase.capAdd + "\n"
			}
			if testCase.capDrop != "" {
				raw += "    cap_drop: " + testCase.capDrop + "\n"
			}

			definition, err := compose.Parse([]byte(raw))
			require.NoError(t, err)
			analyzer := &ServiceCapabilitiesAnalyzer{now: func() time.Time { return now }}
			stack := recommendationTestStack(definition.Services...)

			recommendations := analyzer.Analyze(context.Background(), stack)

			require.Len(t, recommendations, len(testCase.wantTypes), "unexpected recommendation count")
			for index, recommendation := range recommendations {
				assert.Equal(t, testCase.wantTypes[index], recommendation.Type)
				assert.Equal(t, testCase.wantSeverities[index], recommendation.Severity)
				assert.Equal(t, model.Subject{Stack: "payments", Service: "api"}, recommendation.Subject)
				assert.Equal(t, model.Source{File: "compose.yaml", Digest: "compose-digest", Commit: "abc123"}, recommendation.Source)
				assert.Equal(t, now, recommendation.CreatedAt)
			}
		})
	}
}
