package handlers

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"testing"
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
	listPayload, err := json.Marshal(response)
	require.NoError(t, err)
	assert.NotContains(t, string(listPayload), "changes", "list response must remain lightweight")
	detail, err := h.GetDeployment(ctx, generated.GetDeploymentParams{ID: attempt.ID})
	require.NoError(t, err)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"do-not-expose", "api-command-secret", "health-secret", "init-secret"} {
		assert.NotContains(t, string(encoded), secret)
	}
	for _, internalField := range []string{"\"path\"", "Map", "Args", "Extra"} {
		assert.NotContains(t, string(encoded), internalField)
	}
	_, err = h.GetDeployment(ctx, generated.GetDeploymentParams{ID: "unknown"})
	require.Error(t, err)
}
