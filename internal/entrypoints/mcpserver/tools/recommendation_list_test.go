package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestRecommendationListExecute(t *testing.T) {
	ctx := context.Background()
	store := newRecommendationStore(t, ctx)
	require.NoError(t, store.UpdateStack(ctx, "api", []model.Recommendation{
		{
			Severity:       model.SeverityHigh,
			Type:           model.TypeServiceResourcesUnspecified,
			Source:         model.Source{File: "compose.api.yml", Digest: "sha256:api", Commit: "abc123"},
			Subject:        model.Subject{Service: "web"},
			Title:          "Service resources are not specified",
			Recommendation: "Specify deploy.resources for the service.",
			CreatedAt:      time.Date(2026, time.March, 27, 10, 0, 0, 0, time.UTC),
		},
	}), "store api recommendations")
	require.NoError(t, store.UpdateStack(ctx, "worker", []model.Recommendation{
		{
			Severity:       model.SeverityMedium,
			Type:           model.TypeServiceImageDigestUnspecified,
			Source:         model.Source{File: "compose.worker.yml", Digest: "sha256:worker", Commit: "def456"},
			Subject:        model.Subject{Service: "jobs"},
			Title:          "Image digest is not pinned",
			Recommendation: "Pin the service image with @sha256:...",
			CreatedAt:      time.Date(2026, time.March, 27, 11, 0, 0, 0, time.UTC),
		},
	}), "store worker recommendations")

	tool := NewRecommendationList(store)
	response, err := tool.Execute(ctx, routing.Request{
		Payload: recommendationListRequest{
			Stack: " api ",
			Limit: intPointer(1),
		},
	})
	require.NoError(t, err, "execute recommendation_list")

	var payload struct {
		Recommendations []model.Recommendation `json:"recommendations"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	require.Len(t, payload.Recommendations, 1, "unexpected recommendations count")
	assert.Equal(t, "api", payload.Recommendations[0].Subject.Stack, "unexpected stack")
	assert.Equal(t, "web", payload.Recommendations[0].Subject.Service, "unexpected service")
	assert.Equal(t, "Specify deploy.resources for the service.", payload.Recommendations[0].Recommendation, "unexpected recommendation")
}

func TestRecommendationListExecuteFailsOnInvalidLimit(t *testing.T) {
	tool := NewRecommendationList(newRecommendationStore(t, context.Background()))

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: recommendationListRequest{
			Limit: intPointer(maxRecommendationsLimit + 1),
		},
	})
	require.Error(t, err, "expected limit error")
	assert.Contains(t, err.Error(), "limit must be <=", "unexpected error")
}

func newRecommendationStore(t *testing.T, ctx context.Context) *modelstore.FileStore {
	t.Helper()

	store, err := modelstore.NewFileStore(ctx, filepath.Join(t.TempDir(), "recommendations.json"), fs.NewLocalFileSystem())
	require.NoError(t, err, "new recommendations store")

	return store
}
