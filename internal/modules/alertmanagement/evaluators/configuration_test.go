package evaluators

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestConfigurationEvaluatorReturnsActionsWithoutChangingStore(t *testing.T) {
	ctx := t.Context()
	db := testutil.OpenSQLite(t)
	store := modelstore.NewSQLStore(db)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(`
stacks:
  - name: kept
    composeFile: kept.yaml
`), 0o600))
	cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{Stacks: config.StacksSourceSpec{File: "stacks.yaml"}}}}
	_, err := cfg.Reload(dir)
	require.NoError(t, err)

	at := time.Now().UTC()
	removed := model.Alert{
		ID: "incident", Fingerprint: model.SyncFailedFingerprint("removed"), Kind: model.AlertKindSyncFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: "removed", Status: model.AlertStatusOpen,
		OpenedAt: at, UpdatedAt: at, Occurrences: 1,
	}
	require.NoError(t, store.Create(ctx, removed))
	partial := model.SyncFailedFingerprint("partial")
	_, err = store.IncrementFailureStreak(ctx, partial)
	require.NoError(t, err)

	evaluator := NewConfigurationEvaluator(store, cfg)
	actions, err := evaluator.Evaluate(ctx, events.Envelope{ID: "configuration", Event: &events.ConfigurationChanged{}})
	require.NoError(t, err)
	assert.Equal(t, []model.AlertAction{
		model.NewResetFailureStreaksExceptAction([]string{model.SyncFailedFingerprint("kept")}),
		model.NewResolveAction(removed.Fingerprint, "configuration",
			model.ResolutionReasonResourceRemoved, "Stack removed from desired configuration"),
	}, actions)
	stillOpen, err := store.FindOpenByFingerprint(ctx, removed.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, removed.ID, stillOpen.ID)
	var failures uint64
	require.NoError(t, db.Get(ctx, &failures, "SELECT failures FROM alert_failure_streaks WHERE fingerprint = ?", partial))
	assert.EqualValues(t, 1, failures)
}
