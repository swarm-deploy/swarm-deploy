package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestAlertListFiltersDefaultsAndRepresentations(t *testing.T) {
	store := alertstore.NewSQLStore(testutil.OpenSQLite(t))
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	resolvedAt := now.Add(time.Minute)
	alerts := []alertmodel.Alert{
		{ID: "open-stack", Fingerprint: "deploy_failed:stack:api", Kind: alertmodel.AlertKindDeployFailed,
			ResourceType: alertmodel.ResourceTypeStack, ResourceID: "api", Status: alertmodel.AlertStatusOpen,
			Title: "Deployment failed", Message: "apply failed", FailureStage: "apply", ErrorCode: "apply_failed",
			Commit: "abc", Occurrences: 2, OpenedAt: now, UpdatedAt: now,
			OpenEventID: "event-open", LatestEventID: "event-latest"},
		{ID: "open-node", Fingerprint: "node_disconnected:node:node-1", Kind: alertmodel.AlertKindNodeDisconnected,
			ResourceType: alertmodel.ResourceTypeNode, ResourceID: "node-1", Status: alertmodel.AlertStatusOpen,
			Title: "Node disconnected", Occurrences: 1, OpenedAt: now, UpdatedAt: now.Add(time.Second)},
		{ID: "resolved-stack", Fingerprint: "deploy_failed:stack:web", Kind: alertmodel.AlertKindDeployFailed,
			ResourceType: alertmodel.ResourceTypeStack, ResourceID: "web", Status: alertmodel.AlertStatusResolved,
			Title: "Deployment failed", DeploymentID: "deployment-1", Occurrences: 1, OpenedAt: now,
			UpdatedAt: resolvedAt, ResolvedAt: &resolvedAt,
			Resolution: &alertmodel.AlertResolution{Reason: alertmodel.ResolutionReasonRecovered, Message: "recovered"}},
	}
	for _, alert := range alerts {
		require.NoError(t, store.Create(t.Context(), alert))
	}
	tool := NewAlertList(store)

	testCases := []struct {
		name    string
		request alertListRequest
		wantIDs []string
	}{
		{name: "default open", request: alertListRequest{}, wantIDs: []string{"open-node", "open-stack"}},
		{name: "resolved", request: alertListRequest{Status: "resolved"}, wantIDs: []string{"resolved-stack"}},
		{name: "all kind", request: alertListRequest{Status: "all", Kind: string(alertmodel.AlertKindDeployFailed)},
			wantIDs: []string{"resolved-stack", "open-stack"}},
		{name: "resource type", request: alertListRequest{ResourceType: string(alertmodel.ResourceTypeNode)},
			wantIDs: []string{"open-node"}},
		{name: "resource id", request: alertListRequest{ResourceID: "api"}, wantIDs: []string{"open-stack"}},
		{name: "empty", request: alertListRequest{ResourceID: "missing"}, wantIDs: []string{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := tool.Execute(t.Context(), routing.Request{Payload: testCase.request})
			require.NoError(t, err)
			payload := response.Payload.(struct {
				Alerts []alertSummaryResponse `json:"alerts"`
			})
			ids := make([]string, 0, len(payload.Alerts))
			for _, alert := range payload.Alerts {
				ids = append(ids, alert.ID)
			}
			assert.Equal(t, testCase.wantIDs, ids)
		})
	}

	response, err := tool.Execute(t.Context(), routing.Request{Payload: alertListRequest{Status: "resolved"}})
	require.NoError(t, err)
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"alerts": [{
			"id": "resolved-stack",
			"status": "resolved",
			"kind": "deploy_failed",
			"resource_type": "stack",
			"resource_id": "web",
			"title": "Deployment failed",
			"updated_at": "2026-10-11T12:01:00Z",
			"deployment_id": "deployment-1"
		}]
	}`, string(encoded))
	assert.NotContains(t, string(encoded), "message")
	assert.NotContains(t, string(encoded), "fingerprint")
	assert.NotContains(t, string(encoded), "resolution")
	assert.NotContains(t, string(encoded), "occurrences")
	assert.NotContains(t, string(encoded), "event")

	response, err = tool.Execute(t.Context(), routing.Request{Payload: alertListRequest{ResourceID: "api"}})
	require.NoError(t, err)
	encoded, err = json.Marshal(response.Payload)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "deployment_id", "an alert without a deployment must not invent a correlation")
	assert.Contains(t, string(encoded), `"error_code":"apply_failed"`)

	response, err = NewAlertGet(store).Execute(t.Context(), routing.Request{Payload: alertGetRequest{ID: "open-stack"}})
	require.NoError(t, err)
	encoded, err = json.Marshal(response.Payload)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"alert": {
			"id": "open-stack",
			"fingerprint": "deploy_failed:stack:api",
			"kind": "deploy_failed",
			"resource_type": "stack",
			"resource_id": "api",
			"status": "open",
			"title": "Deployment failed",
			"message": "apply failed",
			"failure_stage": "apply",
			"error_code": "apply_failed",
			"commit": "abc",
			"occurrences": 2,
			"opened_at": "2026-10-11T12:00:00Z",
			"updated_at": "2026-10-11T12:00:00Z"
		}
	}`, string(encoded))
	assert.NotContains(t, string(encoded), "event-open")
	assert.NotContains(t, string(encoded), "event-latest")

	response, err = NewAlertGet(store).Execute(t.Context(), routing.Request{Payload: alertGetRequest{ID: "resolved-stack"}})
	require.NoError(t, err)
	encoded, err = json.Marshal(response.Payload)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"deployment_id":"deployment-1"`)
	assert.Contains(t, string(encoded), `"resolved_at":"2026-10-11T12:01:00Z"`)
	assert.Contains(t, string(encoded), `"resolution":{"reason":"recovered","message":"recovered"}`)
}

func TestAlertToolsValidateLimitsAndNotFound(t *testing.T) {
	store := alertstore.NewSQLStore(testutil.OpenSQLite(t))
	tool := NewAlertList(store)
	for _, limit := range []int{0, maxAlertLimit + 1} {
		t.Run("limit", func(t *testing.T) {
			_, err := tool.Execute(t.Context(), routing.Request{Payload: alertListRequest{Limit: &limit}})
			require.Error(t, err)
		})
	}
	_, err := NewAlertGet(store).Execute(t.Context(), routing.Request{Payload: alertGetRequest{ID: "missing"}})
	require.ErrorIs(t, err, alertstore.ErrAlertNotFound)
}
