package recommendations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestServiceSecretsRecommendationLifecycle(t *testing.T) {
	ctx := t.Context()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	recommender := NewRecommender(analyzer.NewServiceSecretsAnalyzer(), store)
	const sentinel = "credential-must-not-be-persisted"
	stack := model.Stack{Name: "database", Definition: compose.File{Path: "compose.yaml", Compose: compose.Compose{
		Services: compose.Services{{Name: "db", Image: "postgres:18", Environment: compose.Environment{
			Map: map[string]string{"POSTGRES_PASSWORD": sentinel},
		}}},
	}}}
	var original model.Recommendation
	for _, step := range []string{"detected", "repeated", "both forms", "migrated"} {
		t.Run(step, func(t *testing.T) {
			if step == "both forms" {
				stack.Definition.Compose.Services[0].Environment.Map["POSTGRES_PASSWORD_FILE"] = "/run/secrets/password"
			}
			if step == "migrated" {
				delete(stack.Definition.Compose.Services[0].Environment.Map, "POSTGRES_PASSWORD")
			}
			stack.Definition.Digest = step
			require.NoError(t, recommender.Recommend(ctx, stack))
			recs, err := store.List(ctx, modelstore.ListFilter{Stack: stack.Name})
			require.NoError(t, err)
			encoded, err := json.Marshal(recs)
			require.NoError(t, err)
			require.False(t, strings.Contains(string(encoded), sentinel), "credential persisted in recommendation")
			if step == "migrated" {
				require.Empty(t, recs)
				return
			}
			require.Len(t, recs, 1)
			if step == "detected" {
				original = recs[0]
			}
			require.Equal(t, original.ID(), recs[0].ID())
			require.True(t, original.CreatedAt.Equal(recs[0].CreatedAt), "creation time must remain stable")
			require.Equal(t, step, recs[0].Source.Digest)
		})
	}
}

func TestRecommenderClearsStackRecommendationsWhenAnalyzerReturnsEmptyList(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))

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
