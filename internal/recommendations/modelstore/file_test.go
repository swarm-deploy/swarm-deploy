package modelstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestFileStoreUpdateStackPersistsAndFiltersRecommendations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recommendations.json")
	store, err := NewFileStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err, "new store")

	require.NoError(t, store.UpdateStack(ctx, "api", []model.Recommendation{
		{
			Severity:       model.SeverityMedium,
			Type:           model.TypeServiceResourcesUnspecified,
			Recommendation: "set resources",
		},
	}), "update api recommendations")
	require.NoError(t, store.UpdateStack(ctx, "worker", []model.Recommendation{
		{
			Severity:       model.SeverityHigh,
			Type:           model.TypeServiceResourcesLimitsUnspecified,
			Recommendation: "set limits",
		},
	}), "update worker recommendations")

	apiRecommendations, err := store.List(ctx, ListFilter{Stack: "api"})
	require.NoError(t, err, "list api recommendations")
	require.Len(t, apiRecommendations, 1, "expected one api recommendation")
	assert.Equal(t, "api", apiRecommendations[0].Subject.Stack, "expected stack in subject")
	assert.Equal(t, "set resources", apiRecommendations[0].Recommendation, "expected api message")

	allRecommendations, err := store.List(ctx, ListFilter{})
	require.NoError(t, err, "list all recommendations")
	require.Len(t, allRecommendations, 2, "expected all recommendations")

	reloaded, err := NewFileStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err, "reload store")

	workerRecommendations, err := reloaded.List(ctx, ListFilter{Stack: "worker"})
	require.NoError(t, err, "list worker recommendations")
	require.Len(t, workerRecommendations, 1, "expected persisted worker recommendation")
	assert.Equal(t, "worker", workerRecommendations[0].Subject.Stack, "expected persisted stack in subject")
	assert.Equal(t, model.SeverityHigh, workerRecommendations[0].Severity, "expected persisted severity")
}

func TestFileStoreUpdateStackReplacesStackRecommendationsAndRebuildsIndexes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recommendations.json")
	store, err := NewFileStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err, "new store")

	require.NoError(t, store.UpdateStack(ctx, "api", []model.Recommendation{
		{Severity: model.SeverityLow, Type: model.TypeServiceResourcesUnspecified, Recommendation: "old api"},
	}), "update api recommendations")
	require.NoError(t, store.UpdateStack(ctx, "worker", []model.Recommendation{
		{Severity: model.SeverityHigh, Type: model.TypeServiceResourcesLimitsUnspecified, Recommendation: "worker"},
	}), "update worker recommendations")
	require.NoError(t, store.UpdateStack(ctx, "api", []model.Recommendation{
		{Severity: model.SeverityMedium, Type: model.TypeServiceResourcesLimitsUnspecified, Recommendation: "new api"},
	}), "replace api recommendations")

	apiRecommendations, err := store.List(ctx, ListFilter{Stack: "api"})
	require.NoError(t, err, "list api recommendations")
	require.Len(t, apiRecommendations, 1, "expected replaced api recommendation")
	assert.Equal(t, "new api", apiRecommendations[0].Recommendation, "expected new api recommendation")

	workerRecommendations, err := store.List(ctx, ListFilter{Stack: "worker"})
	require.NoError(t, err, "list worker recommendations")
	require.Len(t, workerRecommendations, 1, "expected worker recommendation to survive")
	assert.Equal(t, "worker", workerRecommendations[0].Recommendation, "expected worker recommendation")

	payload, err := os.ReadFile(path)
	require.NoError(t, err, "read persisted file")

	var decoded file
	require.NoError(t, json.Unmarshal(payload, &decoded), "decode persisted file")
	require.Len(t, decoded.List, 2, "expected old api recommendation removed")
	assert.Equal(t, []int{0}, decoded.Stacks["worker"], "expected worker index rebuilt")
	assert.Equal(t, []int{1}, decoded.Stacks["api"], "expected api index rebuilt")
}

func TestFileStoreListLimitsRecommendations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recommendations.json")
	store, err := NewFileStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err, "new store")

	require.NoError(t, store.UpdateStack(ctx, "api", []model.Recommendation{
		{Severity: model.SeverityHigh, Type: model.TypeServiceResourcesUnspecified, Recommendation: "api one"},
		{Severity: model.SeverityMedium, Type: model.TypeServiceResourcesLimitsUnspecified, Recommendation: "api two"},
	}), "update api recommendations")
	require.NoError(t, store.UpdateStack(ctx, "worker", []model.Recommendation{
		{Severity: model.SeverityLow, Type: model.TypeServiceResourcesUnspecified, Recommendation: "worker one"},
	}), "update worker recommendations")

	testCases := []struct {
		name      string
		filter    ListFilter
		wantTexts []string
	}{
		{
			name:      "limits all recommendations",
			filter:    ListFilter{Limit: 2},
			wantTexts: []string{"api one", "api two"},
		},
		{
			name:      "limits stack recommendations",
			filter:    ListFilter{Stack: "api", Limit: 1},
			wantTexts: []string{"api one"},
		},
		{
			name:      "non-positive limit returns all matches",
			filter:    ListFilter{Limit: 0},
			wantTexts: []string{"api one", "api two", "worker one"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			recommendations, listErr := store.List(ctx, testCase.filter)
			require.NoError(t, listErr, "list recommendations")
			require.Len(t, recommendations, len(testCase.wantTexts), "unexpected recommendation count")

			for index, wantText := range testCase.wantTexts {
				assert.Equal(t, wantText, recommendations[index].Recommendation, "unexpected recommendation")
			}
		})
	}
}
