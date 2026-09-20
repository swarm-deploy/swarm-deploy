package recommendations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestRecommenderClearsStackRecommendationsWhenAnalyzerReturnsEmptyList(t *testing.T) {
	ctx := context.Background()
	store, err := modelstore.NewFileStore(
		ctx,
		filepath.Join(t.TempDir(), "recommendations.json"),
		fs.NewLocalFileSystem(),
	)
	require.NoError(t, err, "new recommendation store")

	recommender := NewRecommender(analyzer.NewResourcesUnspecifiedAnalyzer(), store)

	require.NoError(t, recommender.Recommend(ctx, model.Stack{
		Name: "api",
		Definition: compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{
						Name:  "api",
						Image: "example/api:latest",
					},
				},
			},
		},
	}), "recommend stack without resources")

	recommendations, err := store.List(ctx, modelstore.ListFilter{Stack: "api"})
	require.NoError(t, err, "list recommendations")
	require.Len(t, recommendations, 1, "expected initial recommendation")

	require.NoError(t, recommender.Recommend(ctx, model.Stack{
		Name: "api",
		Definition: compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{
						Name:  "api",
						Image: "example/api:latest",
						Deploy: compose.ServiceDeploy{
							Resources: &compose.ServiceDeployResources{
								Limits: &compose.ServiceDeployResource{
									Cpus:   "0.50",
									Memory: "128M",
								},
							},
						},
					},
				},
			},
		},
	}), "recommend stack with resources")

	recommendations, err = store.List(ctx, modelstore.ListFilter{Stack: "api"})
	require.NoError(t, err, "list recommendations after cleanup")
	require.Empty(t, recommendations, "expected recommendations to be cleared")
}
