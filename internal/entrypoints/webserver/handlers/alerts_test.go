package handlers

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestAlertsAPIReadsPersistedAlerts(t *testing.T) {
	ctx := context.Background()
	store, err := modelstore.NewFileStore(ctx, filepath.Join(t.TempDir(), "alerts.json"), sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "create store")
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	open := alertmodel.Alert{ID: "open-id", Fingerprint: "open", Status: alertmodel.AlertStatusOpen, Kind: alertmodel.AlertKindDeployFailed, ResourceType: alertmodel.ResourceTypeStack, ResourceID: "api", Title: "Deployment failed", Message: "failed", Occurrences: 1, OpenedAt: now, UpdatedAt: now, OpenEventID: "event", LatestEventID: "event"}
	resolvedAt := now.Add(time.Minute)
	resolved := open
	resolved.ID = "resolved-id"
	resolved.Fingerprint = "resolved"
	resolved.Status = alertmodel.AlertStatusResolved
	resolved.ResolvedAt = &resolvedAt
	resolved.Resolution = &alertmodel.AlertResolution{Reason: alertmodel.ResolutionReasonRecovered, Message: "Deployment completed successfully", EventID: "recovery"}
	require.NoError(t, store.Create(ctx, open), "persist open alert")
	require.NoError(t, store.Create(ctx, resolved), "persist resolved alert")
	h := &handler{alerts: store}

	response, err := h.ListAlerts(ctx, generated.ListAlertsParams{Status: generated.NewOptAlertStatus(generated.AlertStatusOpen)})
	require.NoError(t, err, "list open alerts")
	require.Len(t, response.Alerts, 1, "status filter")
	assert.Equal(t, "open-id", response.Alerts[0].ID, "persisted alert should be returned")

	details, err := h.GetAlert(ctx, generated.GetAlertParams{ID: "resolved-id"})
	require.NoError(t, err, "get alert details")
	assert.Equal(t, "resolved-id", details.ID, "requested alert")
	assert.True(t, details.Resolution.IsSet(), "resolution should be mapped")
}
