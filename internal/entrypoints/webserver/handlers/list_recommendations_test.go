package handlers

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestHandlerListRecommendations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := modelstore.NewFileStore(ctx, filepath.Join(t.TempDir(), "recommendations.json"), fs.NewLocalFileSystem())
	require.NoError(t, err, "new recommendations store")
	createdAt := time.Date(2026, 9, 18, 10, 11, 12, 0, time.UTC)

	require.NoError(t, store.UpdateStack(ctx, "api", []model.Recommendation{
		{
			Severity: model.SeverityHigh,
			Type:     model.TypeServiceResourcesUnspecified,
			Source: model.Source{
				File:   "compose.yaml",
				Digest: "sha256:api",
				Commit: "abc123",
			},
			Subject: model.Subject{
				Service: "web",
			},
			Title:          "Missing resources",
			Recommendation: "Set CPU and memory requests for web.",
			CreatedAt:      createdAt,
		},
	}), "store api recommendations")
	require.NoError(t, store.UpdateStack(ctx, "worker", []model.Recommendation{
		{
			Severity: model.SeverityMedium,
			Type:     model.TypeServiceResourcesLimitsUnspecified,
			Source: model.Source{
				File:   "worker.yaml",
				Digest: "sha256:worker",
				Commit: "def456",
			},
			Subject: model.Subject{
				Service: "jobs",
			},
			Title:          "Missing resource limits",
			Recommendation: "Set CPU and memory limits for jobs.",
			CreatedAt:      createdAt.Add(time.Minute),
		},
	}), "store worker recommendations")

	testCases := []struct {
		name      string
		stack     string
		limit     int32
		wantTexts []string
	}{
		{
			name:      "all recommendations",
			wantTexts: []string{"Set CPU and memory requests for web.", "Set CPU and memory limits for jobs."},
		},
		{
			name:      "filtered by stack",
			stack:     "api",
			wantTexts: []string{"Set CPU and memory requests for web."},
		},
		{
			name:      "limited recommendations",
			limit:     1,
			wantTexts: []string{"Set CPU and memory requests for web."},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			params := generated.ListRecommendationsParams{}
			if testCase.stack != "" {
				params.Stack.SetTo(testCase.stack)
			}
			if testCase.limit > 0 {
				params.Limit.SetTo(testCase.limit)
			}

			resp, listErr := (&handler{recommendations: store}).ListRecommendations(ctx, params)
			require.NoError(t, listErr, "list recommendations")
			require.NotNil(t, resp, "response must be set")
			require.Len(t, resp.Recommendations, len(testCase.wantTexts), "unexpected recommendation count")

			for index, wantText := range testCase.wantTexts {
				assert.Equal(t, wantText, resp.Recommendations[index].Recommendation, "unexpected recommendation")
				assert.NotEmpty(t, resp.Recommendations[index].Title, "title must be included")
				assert.NotEmpty(t, resp.Recommendations[index].Source.File, "source file must be included")
				assert.NotEmpty(t, resp.Recommendations[index].Source.Digest, "source digest must be included")
				assert.NotEmpty(t, resp.Recommendations[index].Source.Commit, "source commit must be included")
				assert.NotEmpty(t, resp.Recommendations[index].Subject.Stack, "stack must be included")
				assert.NotEmpty(t, resp.Recommendations[index].Subject.Service, "service must be included")
				assert.False(t, resp.Recommendations[index].CreatedAt.IsZero(), "created_at must be included")
			}
		})
	}
}
