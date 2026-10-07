package modelstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestFileStoreReconcilesRetentionOnStartup(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "alerts.json")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	alerts := make([]model.Alert, 0, MaxStoredAlerts+2)
	for index := 0; index < MaxStoredAlerts+1; index++ {
		resolvedAt := base.Add(time.Duration(index) * time.Minute)
		alerts = append(alerts, model.Alert{
			ID: "resolved-" + resolvedAt.Format(time.RFC3339), Fingerprint: "resolved", Status: model.AlertStatusResolved,
			OpenedAt: resolvedAt, UpdatedAt: resolvedAt, ResolvedAt: &resolvedAt,
		})
	}
	alerts = append(alerts, model.Alert{ID: "open", Fingerprint: "open", Status: model.AlertStatusOpen, OpenedAt: base, UpdatedAt: base})
	payload, err := json.Marshal(persistedAlerts{Alerts: alerts})
	require.NoError(t, err, "encode fixture")
	require.NoError(t, os.WriteFile(path, payload, alertFileMode), "write fixture")

	store, err := NewFileStore(ctx, path, sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "load store")
	stored, err := store.List(ctx, ListFilter{})
	require.NoError(t, err, "list retained alerts")
	assert.Len(t, stored, MaxStoredAlerts, "store should be pruned to its limit")
	_, err = store.FindOpenByFingerprint(ctx, "open")
	require.NoError(t, err, "find open alert")
	for _, alert := range stored {
		assert.NotEqual(t, "resolved-"+base.Format(time.RFC3339), alert.ID, "oldest resolved alert should be pruned first")
		assert.NotEqual(t, "resolved-"+base.Add(time.Minute).Format(time.RFC3339), alert.ID, "second-oldest resolved alert should be pruned")
	}

	reloaded, err := NewFileStore(ctx, path, sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "reload reconciled store")
	reloadedAlerts, err := reloaded.List(ctx, ListFilter{})
	require.NoError(t, err, "list after restart")
	assert.Len(t, reloadedAlerts, MaxStoredAlerts, "reconciled retention should persist")
}

func TestFileStorePreservesOpenAlertsAboveLimit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "alerts.json")
	alerts := make([]model.Alert, 0, MaxStoredAlerts+1)
	for index := 0; index < MaxStoredAlerts+1; index++ {
		alerts = append(alerts, model.Alert{ID: string(rune(index + 1)), Fingerprint: string(rune(index + 1)), Status: model.AlertStatusOpen})
	}
	payload, err := json.Marshal(persistedAlerts{Alerts: alerts})
	require.NoError(t, err, "encode fixture")
	require.NoError(t, os.WriteFile(path, payload, alertFileMode), "write fixture")
	store, err := NewFileStore(ctx, path, sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "load store")
	stored, err := store.List(ctx, ListFilter{})
	require.NoError(t, err, "list alerts")
	assert.Len(t, stored, MaxStoredAlerts+1, "active incidents must be preserved")
}
