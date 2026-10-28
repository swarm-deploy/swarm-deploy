package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestAlertsAPIReadsPersistedAlerts(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	open := alertmodel.Alert{ID: "open-id", Fingerprint: "open", Status: alertmodel.AlertStatusOpen, Kind: alertmodel.AlertKindSyncFailed, ResourceType: alertmodel.ResourceTypeStack, ResourceID: "api", Title: "Synchronization failed", Message: "image missing", FailureStage: "apply", ErrorCode: "apply_failed", DeploymentID: "deployment", Commit: "sha", Occurrences: 1, OpenedAt: now, UpdatedAt: now, OpenEventID: "event", LatestEventID: "event"}
	resolvedAt := now.Add(time.Minute)
	resolved := open
	resolved.ID = "resolved-id"
	resolved.Fingerprint = "resolved"
	resolved.Status = alertmodel.AlertStatusResolved
	resolved.ResolvedAt = &resolvedAt
	resolved.Resolution = &alertmodel.AlertResolution{Reason: alertmodel.ResolutionReasonResourceRemoved, Message: "Stack removed from desired configuration", EventID: "configuration"}
	require.NoError(t, store.Create(ctx, open), "persist open alert")
	require.NoError(t, store.Create(ctx, resolved), "persist resolved alert")
	h := &handler{alerts: store}

	response, err := h.ListAlerts(ctx, generated.ListAlertsParams{Status: generated.NewOptAlertStatus(generated.AlertStatusOpen)})
	require.NoError(t, err, "list open alerts")
	require.Len(t, response.Alerts, 1, "status filter")
	assert.Equal(t, "open-id", response.Alerts[0].ID, "persisted alert should be returned")
	assert.Equal(t, "apply", response.Alerts[0].FailureStage.Value)
	assert.Equal(t, "apply_failed", response.Alerts[0].ErrorCode.Value)
	assert.Equal(t, "deployment", response.Alerts[0].DeploymentId.Value)

	details, err := h.GetAlert(ctx, generated.GetAlertParams{ID: "resolved-id"})
	require.NoError(t, err, "get alert details")
	assert.Equal(t, "resolved-id", details.ID, "requested alert")
	assert.True(t, details.Resolution.IsSet(), "resolution should be mapped")
	assert.Equal(t, generated.AlertResolutionReasonResourceRemoved, details.Resolution.Value.Reason)
}

func TestAlertsAPIStackFilter(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for index, resource := range []struct {
		name string
		kind alertmodel.ResourceType
	}{
		{name: "app", kind: alertmodel.ResourceTypeStack},
		{name: "other", kind: alertmodel.ResourceTypeStack},
		{name: "app", kind: alertmodel.ResourceTypeNode},
	} {
		id := fmt.Sprint(index)
		require.NoError(t, store.Create(ctx, alertmodel.Alert{
			ID: id, Fingerprint: id, Kind: alertmodel.AlertKindSyncFailed,
			ResourceType: resource.kind, ResourceID: resource.name, Status: alertmodel.AlertStatusOpen,
			Title: "Failure", Message: "Unavailable", Occurrences: 1,
			OpenedAt: now, UpdatedAt: now.Add(time.Duration(index) * time.Minute), OpenEventID: id, LatestEventID: id,
		}))
	}
	h := &handler{alerts: store}
	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)
	for _, testCase := range []struct {
		query string
		ids   []string
	}{
		{query: "?stack=app&status=open&limit=1", ids: []string{"0"}},
		{query: "?stack=other&status=open", ids: []string{"1"}},
		{query: "?stack=unknown", ids: []string{}},
		{query: "?stack=app&status=resolved", ids: []string{}},
		{query: "?status=open", ids: []string{"2", "1", "0"}},
	} {
		t.Run(testCase.query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/alerts"+testCase.query, nil))
			require.Equal(t, http.StatusOK, recorder.Code)
			var response generated.AlertsResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			ids := make([]string, 0, len(response.Alerts))
			for _, alert := range response.Alerts {
				ids = append(ids, alert.ID)
			}
			assert.Equal(t, testCase.ids, ids)
		})
	}
}
