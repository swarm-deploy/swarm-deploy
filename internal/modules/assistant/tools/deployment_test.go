package tools

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestDeploymentToolsListFilterPaginationAndSafeDetail(t *testing.T) {
	db := testutil.OpenSQLite(t)
	service := deployment.NewService(db, &dispatcher.NopDispatcher{})
	store := deployment.NewStore(db)
	failedIDs := make([]string, 0, 3)
	for index, stack := range []string{"api", "other", "api", "api"} {
		file := deploymentToolCompose(index)
		attempt, err := service.StartIfChanged(t.Context(), stack, string(rune('a'+index)), file, true)
		require.NoError(t, err)
		if index == 1 {
			require.NoError(t, service.Succeed(t.Context(), attempt.ID, gitopsmodel.Stack{}))
			continue
		}
		failedIDs = append(failedIDs, attempt.ID)
		require.NoError(t, service.FailApplyWithError(t.Context(), attempt.ID, gitopsmodel.Stack{},
			"apply_failed", errors.New("registry request failed: token=private-value")))
	}
	running, err := service.StartIfChanged(t.Context(), "running", "running", deploymentToolCompose(10), true)
	require.NoError(t, err)
	require.NotNil(t, running)
	interrupted, err := service.StartIfChanged(t.Context(), "interrupted", "interrupted", deploymentToolCompose(11), true)
	require.NoError(t, err)
	require.NoError(t, service.InterruptStack(t.Context(), "interrupted"))
	require.NotNil(t, interrupted)

	list := NewDeploymentList(store)
	limit := 2
	first, err := list.Execute(t.Context(), routing.Request{Payload: deploymentListRequest{
		Stack: "api", Status: "failed", Limit: &limit,
	}})
	require.NoError(t, err)
	firstPayload := first.Payload.(struct {
		Deployments []deploymentSummaryResponse `json:"deployments"`
		NextCursor  string                      `json:"next_cursor,omitempty"`
	})
	require.Len(t, firstPayload.Deployments, 2)
	require.NotEmpty(t, firstPayload.NextCursor)
	for _, item := range firstPayload.Deployments {
		assert.Equal(t, deployment.Failed, item.Status)
		assert.Equal(t, "api", item.Stack)
		assert.NotNil(t, item.DurationMS)
	}
	second, err := list.Execute(t.Context(), routing.Request{Payload: deploymentListRequest{
		Stack: "api", Status: "failed", Limit: &limit, Cursor: firstPayload.NextCursor,
	}})
	require.NoError(t, err)
	secondPayload := second.Payload.(struct {
		Deployments []deploymentSummaryResponse `json:"deployments"`
		NextCursor  string                      `json:"next_cursor,omitempty"`
	})
	require.Len(t, secondPayload.Deployments, 1)
	assert.Empty(t, secondPayload.NextCursor)

	statuses := []deployment.Status{deployment.Running, deployment.Succeeded, deployment.Interrupted}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			response, listErr := list.Execute(t.Context(), routing.Request{Payload: deploymentListRequest{Status: string(status)}})
			require.NoError(t, listErr)
			payload := response.Payload.(struct {
				Deployments []deploymentSummaryResponse `json:"deployments"`
				NextCursor  string                      `json:"next_cursor,omitempty"`
			})
			require.NotEmpty(t, payload.Deployments)
			for _, item := range payload.Deployments {
				assert.Equal(t, status, item.Status)
			}
		})
	}

	detail, err := NewDeploymentGet(store).Execute(t.Context(), routing.Request{Payload: deploymentGetRequest{ID: failedIDs[0]}})
	require.NoError(t, err)
	encoded, err := json.Marshal(detail.Payload)
	require.NoError(t, err)
	jsonResult := string(encoded)
	assert.Contains(t, jsonResult, `"error_diagnostic":"registry request failed: token=****"`)
	assert.Contains(t, jsonResult, `"redacted":true`)
	assert.NotContains(t, jsonResult, "private-value")
	assert.NotContains(t, jsonResult, "plaintext-secret")
	assert.NotContains(t, jsonResult, "configuration")
	assert.NotContains(t, jsonResult, "compose")
	assert.NotContains(t, jsonResult, "fingerprints")
}

func TestDeploymentToolsValidateLimitsStatusAndNotFound(t *testing.T) {
	store := deployment.NewStore(testutil.OpenSQLite(t))
	list := NewDeploymentList(store)
	for _, request := range []deploymentListRequest{
		{Status: "unknown"},
		{Limit: intPointer(0)},
		{Limit: intPointer(maxDeploymentLimit + 1)},
	} {
		_, err := list.Execute(t.Context(), routing.Request{Payload: request})
		require.Error(t, err)
	}
	_, err := NewDeploymentGet(store).Execute(t.Context(), routing.Request{Payload: deploymentGetRequest{ID: "missing"}})
	require.ErrorIs(t, err, deployment.ErrNotFound)
}

func deploymentToolCompose(index int) compose.File {
	return compose.File{Path: "stack.yaml", Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "api:" + string(rune('a'+index)),
		Environment: compose.Environment{Map: map[string]string{"API_TOKEN": "plaintext-secret"}},
	}}}}
}
