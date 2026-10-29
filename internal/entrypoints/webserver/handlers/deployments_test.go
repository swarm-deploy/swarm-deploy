package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestDeploymentAPIReadsAttemptsWithoutHistory(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	svc := deployment.NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	replicas := uint64(3)
	attempt, err := svc.StartIfChanged(ctx, "app", "sha", compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "image:1",
		Command:     compose.NewCommand([]string{"sh", "-c", "app --password api-command-secret"}),
		Healthcheck: &compose.ServiceHealth{Test: compose.NewCommand([]string{"CMD-SHELL", "check health-secret"})},
		InitJobs:    []compose.InitJob{{Name: "init", Image: "image:1", Command: []string{"init init-secret"}}},
		Environment: compose.Environment{Map: map[string]string{"API_TOKEN": "do-not-expose"}},
		Deploy:      compose.ServiceDeploy{Replicas: &replicas},
	}, {
		Name: "worker", Image: "worker:1",
	}, {
		Name: "metrics", Image: "metrics:1", Deploy: compose.ServiceDeploy{Mode: "global"},
	}, {
		Name: "batch", Image: "batch:1", Deploy: compose.ServiceDeploy{Mode: "replicated-job", Extra: map[string]interface{}{
			"max_concurrent": 2, "total_completions": 5,
		}},
	}}}}, true)
	require.NoError(t, err)
	h := &handler{deployments: deploymentstore.NewStore(db)}
	response, err := h.ListDeployments(ctx, generated.ListDeploymentsParams{Stack: []string{"app"}})
	require.NoError(t, err)
	require.Len(t, response.Deployments, 1)
	assert.Equal(t, generated.DeploymentStatusRunning, response.Deployments[0].Status)
	assert.True(t, response.Deployments[0].DiffAvailable)
	listPayload, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(listPayload), "changes", "list response must remain lightweight")
	now := time.Now().UTC()
	store := deploymentstore.NewStore(db)
	require.NoError(t, store.SaveInitJobExecution(ctx, deploymentmodel.InitJobExecution{
		AttemptID: attempt.ID, Ordinal: 1, ServiceName: "api", JobName: "init",
		Status: deploymentmodel.InitJobSucceeded, StartedAt: now, FinishedAt: now.Add(time.Second),
		Logs: []byte("raw <script>credential=secret</script>\x00"), LogsAvailable: true, LogsTruncated: true,
	}))
	require.NoError(t, store.SaveInitJobExecution(ctx, deploymentmodel.InitJobExecution{
		AttemptID: attempt.ID, Ordinal: 2, ServiceName: "api", JobName: "optional",
		Status: deploymentmodel.InitJobFailed, StartedAt: now, FinishedAt: now.Add(time.Second),
		LogsAvailable: false,
	}))
	require.NoError(t, svc.FailApplyWithError(ctx, attempt.ID, gitopsmodel.Stack{}, "apply_failed",
		errors.New("update service api: pull https://user:password@registry.example/image: image not found")))
	detail, err := h.GetDeployment(ctx, generated.GetDeploymentParams{ID: attempt.DeploymentID})
	require.NoError(t, err)
	require.Len(t, detail.Attempts, 1)
	assert.Equal(t, attempt.ID, detail.Attempts[0].ID)
	assert.Equal(t, generated.DeploymentAttemptStatusFailed, detail.Attempts[0].Status)
	require.Len(t, detail.Attempts[0].InitJobs, 2)
	assert.Equal(t, []int{1, 2}, []int{
		detail.Attempts[0].InitJobs[0].Ordinal,
		detail.Attempts[0].InitJobs[1].Ordinal,
	})
	assert.True(t, detail.Attempts[0].InitJobs[0].LogsAvailable)
	assert.True(t, detail.Attempts[0].InitJobs[0].LogsTruncated)
	assert.False(t, detail.Attempts[0].InitJobs[1].LogsAvailable)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"do-not-expose", "api-command-secret", "health-secret", "init-secret"} {
		assert.NotContains(t, string(encoded), secret)
	}
	assert.NotContains(t, string(encoded), "credential=secret", "raw logs must not be included in deployment details")

	recorder := httptest.NewRecorder()
	require.NoError(t, h.GetDeploymentInitJobLogs(ctx, generated.GetDeploymentInitJobLogsParams{
		ID: attempt.DeploymentID, AttemptID: attempt.ID, Ordinal: 1,
	}, recorder))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, []byte("raw <script>credential=secret</script>\x00"), recorder.Body.Bytes())

	for _, params := range []generated.GetDeploymentInitJobLogsParams{
		{ID: "other-deployment", AttemptID: attempt.ID, Ordinal: 1},
		{ID: attempt.DeploymentID, AttemptID: "other-attempt", Ordinal: 1},
		{ID: attempt.DeploymentID, AttemptID: attempt.ID, Ordinal: 99},
		{ID: attempt.DeploymentID, AttemptID: attempt.ID, Ordinal: 2},
	} {
		err = h.GetDeploymentInitJobLogs(ctx, params, httptest.NewRecorder())
		var responseErr *statusError
		require.ErrorAs(t, err, &responseErr)
		assert.Equal(t, http.StatusNotFound, responseErr.code)
	}
	require.True(t, detail.Diagnostic.IsSet())
	assert.Contains(t, detail.Diagnostic.Value, "image not found")
	assert.NotContains(t, detail.Diagnostic.Value, "password")
	require.True(t, detail.ConfigurationYaml.IsSet())
	assert.Contains(t, detail.ConfigurationYaml.Value, "services:")
	assert.Contains(t, detail.ConfigurationYaml.Value, "API_TOKEN=****")
	assert.ElementsMatch(t, []generated.DeploymentService{
		{Name: "api", Image: "image:1", Replicas: "3"},
		{Name: "worker", Image: "worker:1", Replicas: "1"},
		{Name: "metrics", Image: "metrics:1", Replicas: "Global"},
		{Name: "batch", Image: "batch:1", Replicas: "Job: 2 concurrent, 5 total"},
	}, detail.Services)
	for _, internalField := range []string{"\"path\"", "Map", "Args", "Extra"} {
		assert.NotContains(t, string(encoded), internalField)
	}
	_, err = db.Exec(ctx, "DELETE FROM deployment_attempts WHERE deployment_id=?", attempt.DeploymentID)
	require.NoError(t, err)
	legacyDetail, err := h.GetDeployment(ctx, generated.GetDeploymentParams{ID: attempt.DeploymentID})
	require.NoError(t, err)
	assert.Empty(t, legacyDetail.LatestAttemptID)
	assert.Zero(t, legacyDetail.AttemptCount)
	assert.Empty(t, legacyDetail.Attempts)
	require.NoError(t, legacyDetail.Validate())
	_, err = h.GetDeployment(ctx, generated.GetDeploymentParams{ID: "unknown"})
	require.Error(t, err)
	listed, err := h.ListDeployments(ctx, generated.ListDeploymentsParams{Stack: []string{"app"}})
	require.NoError(t, err)
	require.Len(t, listed.Deployments, 1)
	require.NoError(t, listed.Deployments[0].Validate())
}

func TestDeploymentServiceReplicas(t *testing.T) {
	three := uint64(3)
	zero := uint64(0)
	for _, tc := range []struct {
		name   string
		deploy compose.ServiceDeploy
		want   string
	}{
		{name: "explicit replicas", deploy: compose.ServiceDeploy{Replicas: &three}, want: "3"},
		{name: "zero replicas", deploy: compose.ServiceDeploy{Replicas: &zero}, want: "0"},
		{name: "default replicas", deploy: compose.ServiceDeploy{Mode: "replicated"}, want: "1"},
		{name: "missing deploy", deploy: compose.ServiceDeploy{}, want: "1"},
		{name: "global", deploy: compose.ServiceDeploy{Mode: "global"}, want: "Global"},
		{name: "global job", deploy: compose.ServiceDeploy{Mode: "global-job"}, want: "Global job"},
		{name: "replicated job", deploy: compose.ServiceDeploy{Mode: "replicated-job", Extra: map[string]interface{}{
			"max_concurrent": float64(2), "total_completions": float64(5),
		}}, want: "Job: 2 concurrent, 5 total"},
		{name: "incomplete job", deploy: compose.ServiceDeploy{Mode: "replicated-job"}, want: "Replicated job"},
		{name: "unknown mode", deploy: compose.ServiceDeploy{Mode: "unknown"}, want: "—"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, deploymentServiceReplicas(tc.deploy))
		})
	}
}

func TestDeploymentFiltersUsesAllRecordedStacks(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	h := &handler{deployments: deploymentstore.NewStore(db)}
	service := deployment.NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	for _, tc := range []struct {
		stack   string
		commit  string
		service string
	}{
		{stack: "zeta", commit: "one", service: "api"},
		{stack: "alpha", commit: "two", service: "worker"},
	} {
		_, err := service.StartIfChanged(ctx, tc.stack, tc.commit, compose.File{Compose: compose.Compose{
			Services: compose.Services{{Name: tc.service, Image: "image:1"}},
		}}, true)
		require.NoError(t, err)
	}
	filters, err := h.GetDeploymentFilters(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "zeta"}, filters.Stacks)
	assert.Equal(t, []generated.DeploymentFilterService{
		{StackName: "alpha", ServiceName: "worker"},
		{StackName: "zeta", ServiceName: "api"},
	}, filters.Services)
	listed, err := h.ListDeployments(ctx, generated.ListDeploymentsParams{ServiceName: []string{"worker"}})
	require.NoError(t, err)
	require.Len(t, listed.Deployments, 1)
	assert.Equal(t, "alpha", listed.Deployments[0].Stack)
	listed, err = h.ListDeployments(ctx, generated.ListDeploymentsParams{
		Stack: []string{"alpha", "zeta"}, ServiceName: []string{"worker", "api"},
	})
	require.NoError(t, err)
	require.Len(t, listed.Deployments, 2)
	assert.ElementsMatch(t, []string{"alpha", "zeta"}, []string{
		listed.Deployments[0].Stack, listed.Deployments[1].Stack,
	})
}
