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
	attempt, err := svc.StartIfChanged(ctx, "app", "sha", compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "image:1",
		Command:     compose.NewCommand([]string{"sh", "-c", "app --password api-command-secret"}),
		Healthcheck: &compose.ServiceHealth{Test: compose.NewCommand([]string{"CMD-SHELL", "check health-secret"})},
		InitJobs:    []compose.InitJob{{Name: "init", Image: "image:1", Command: []string{"init init-secret"}}},
		Environment: compose.Environment{Map: map[string]string{"API_TOKEN": "do-not-expose"}},
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
	detail, err := h.GetDeployment(ctx, generated.GetDeploymentParams{ID: attempt.ID})
	require.NoError(t, err)
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
	for _, internalField := range []string{"\"path\"", "Map", "Args", "Extra"} {
		assert.NotContains(t, string(encoded), internalField)
	}
	_, err = h.GetDeployment(ctx, generated.GetDeploymentParams{ID: "unknown"})
	require.Error(t, err)
	_, err = db.Get(ctx).ExecContext(ctx,
		"UPDATE deployments SET payload=json_set(payload,'$.status','succeeded') WHERE id=?", attempt.ID)
	require.NoError(t, err)
	_, err = h.ListDeployments(ctx, generated.ListDeploymentsParams{Stack: generated.NewOptString("app")})
	require.ErrorIs(t, err, deployment.ErrInconsistentDeployment)
	_, err = h.GetDeployment(ctx, generated.GetDeploymentParams{ID: attempt.ID})
	require.ErrorIs(t, err, deployment.ErrInconsistentDeployment)
}
