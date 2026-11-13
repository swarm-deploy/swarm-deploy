package recommendations

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestRecommenderEventSubscriberLoadsPersistedDesired(t *testing.T) {
	testCases := []struct {
		name       string
		completion deployments.Completion
		event      func(events.DeployEvent) events.Event
	}{
		{
			name:       "successful deployment",
			completion: deployments.Completion{Outcome: deployments.CompletionSucceeded},
			event: func(meta events.DeployEvent) events.Event {
				return &events.DeploySuccess{DeployEvent: meta}
			},
		},
		{
			name: "failed deployment",
			completion: deployments.Completion{
				Outcome: deployments.CompletionApplyFailed, Diagnostic: errors.New("apply failed"),
			},
			event: func(meta events.DeployEvent) events.Event {
				return &events.DeployFailed{DeployEvent: meta}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := testutil.OpenSQLite(t)
			deploymentID := completeRecommendationDeployment(t, db, testCase.completion)
			store := modelstore.NewSQLStore(db)
			subscriber := NewRecommenderEventSubscriber(
				deploymentstore.NewStore(db),
				NewRecommender(analyzer.NewResourcesUnspecifiedAnalyzer(), store),
			)
			meta := recommendationDeployEvent(deploymentID)

			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{Event: testCase.event(meta)}))
			recommendations, err := store.List(t.Context(), modelstore.ListFilter{Stack: "app"})
			require.NoError(t, err)
			require.Len(t, recommendations, 1)
			assert.Equal(t, model.TypeServiceResourcesUnspecified, recommendations[0].Type)
			assert.Equal(t, "persisted-digest", recommendations[0].Source.Digest)
			assert.Equal(t, "event-commit", recommendations[0].Source.Commit)
		})
	}
}

func TestOrderedSubscriberRollsBackVersionForReplay(t *testing.T) {
	db := testutil.OpenSQLite(t)
	deploymentID := completeRecommendationDeployment(t, db, deployments.Completion{
		Outcome: deployments.CompletionSucceeded,
	})
	store := modelstore.NewSQLStore(db)
	subscriber := &orderedSubscriber{
		db: db,
		next: NewRecommenderEventSubscriber(
			deploymentstore.NewStore(db),
			NewRecommender(analyzer.NewResourcesUnspecifiedAnalyzer(), store),
		),
	}
	_, err := db.Exec(t.Context(), `CREATE TRIGGER fail_recommendation_insert
		BEFORE INSERT ON recommendations BEGIN SELECT RAISE(FAIL, 'injected failure'); END`)
	require.NoError(t, err)
	event := events.Envelope{
		ID: "deploy-success", OccurredAt: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC),
		Event: &events.DeploySuccess{DeployEvent: recommendationDeployEvent(deploymentID)},
	}

	require.Error(t, subscriber.Handle(t.Context(), event))
	versions, recommendations := recommendationStateCounts(t, db)
	assert.Zero(t, versions)
	assert.Zero(t, recommendations)
	_, err = db.Exec(t.Context(), "DROP TRIGGER fail_recommendation_insert")
	require.NoError(t, err)
	require.NoError(t, subscriber.Handle(t.Context(), event))
	versions, recommendations = recommendationStateCounts(t, db)
	assert.Equal(t, 1, versions)
	assert.Equal(t, 1, recommendations)
}

func completeRecommendationDeployment(
	t *testing.T,
	db storage.Database,
	completion deployments.Completion,
) string {
	t.Helper()
	lifecycle := deployments.NewService(db, &dispatcher.NopDispatcher{})
	desired := compose.File{Path: "compose.yaml", Digest: "persisted-digest", Compose: compose.Compose{
		Services: compose.Services{{Name: "api", Image: "example/api:1"}},
	}}
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "persisted-commit", desired, true)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, completion))
	return attempt.DeploymentID
}

func recommendationDeployEvent(deploymentID string) events.DeployEvent {
	return events.DeployEvent{
		DeploymentID: deploymentID,
		StackName:    "app",
		Commit:       "event-commit",
		StackDefinition: compose.File{Compose: compose.Compose{Services: compose.Services{{
			Name: "ignored", Deploy: compose.ServiceDeploy{Resources: &compose.ServiceDeployResources{
				Limits: &compose.ServiceDeployResource{Cpus: "1", Memory: "128M"},
			}},
		}}}},
	}
}

func recommendationStateCounts(t *testing.T, db storage.Database) (int, int) {
	t.Helper()
	var counts struct {
		Versions        int `db:"versions"`
		Recommendations int `db:"recommendations"`
	}
	require.NoError(t, db.Get(t.Context(), &counts, `SELECT
		(SELECT count(*) FROM projection_versions) AS versions,
		(SELECT count(*) FROM recommendations) AS recommendations`))
	return counts.Versions, counts.Recommendations
}
