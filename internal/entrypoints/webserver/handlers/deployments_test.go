package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestDeploymentAPIReadsAttemptsWithoutHistory(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	svc := deployment.NewService(db, outbox.New(db))
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
	h := &handler{deployments: deployment.NewStore(db)}
	response, err := h.ListDeployments(ctx, generated.ListDeploymentsParams{Stack: generated.NewOptString("app")})
	require.NoError(t, err)
	require.Len(t, response.Deployments, 1)
	assert.Equal(t, generated.DeploymentStatusRunning, response.Deployments[0].Status)
	assert.True(t, response.Deployments[0].DiffAvailable)
	listPayload, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(listPayload), "changes", "list response must remain lightweight")
	require.NoError(t, svc.FailApplyWithError(ctx, attempt.ID, gitopsmodel.Stack{}, "apply_failed",
		errors.New("update service api: pull https://user:password@registry.example/image: image not found")))
	detail, err := h.GetDeployment(ctx, generated.GetDeploymentParams{ID: attempt.DeploymentID})
	require.NoError(t, err)
	require.Len(t, detail.Attempts, 1)
	assert.Equal(t, attempt.ID, detail.Attempts[0].ID)
	assert.Equal(t, generated.DeploymentAttemptStatusFailed, detail.Attempts[0].Status)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"do-not-expose", "api-command-secret", "health-secret", "init-secret"} {
		assert.NotContains(t, string(encoded), secret)
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
	_, err = h.GetDeployment(ctx, generated.GetDeploymentParams{ID: "unknown"})
	require.Error(t, err)
	listed, err := h.ListDeployments(ctx, generated.ListDeploymentsParams{Stack: generated.NewOptString("app")})
	require.NoError(t, err)
	require.Len(t, listed.Deployments, 1)
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
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	h := &handler{deployments: deployment.NewStore(db)}
	service := deployment.NewService(db, outbox.New(db))
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
	listed, err := h.ListDeployments(ctx, generated.ListDeploymentsParams{ServiceName: generated.NewOptString("worker")})
	require.NoError(t, err)
	require.Len(t, listed.Deployments, 1)
	assert.Equal(t, "alpha", listed.Deployments[0].Stack)
}
