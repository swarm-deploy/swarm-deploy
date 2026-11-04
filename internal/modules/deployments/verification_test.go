package deployments

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestUpdateGenerationSurvivesInterruptedApplyAndDesiredStateChanges(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	lifecycle := NewService(db, &dispatcher.NopDispatcher{})
	desired := desiredServices("api", "worker")
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true)
	require.NoError(t, err)
	generation := deploymentmodel.UpdateGeneration{ServiceID: "api-id", After: time.Now().UTC(), ForceUpdate: 7}
	require.NoError(t, lifecycle.RecordUpdateGeneration(t.Context(), attempt.ID, "api", generation))
	// The process disappears without completion after a service write.
	require.NoError(t, db.Close())
	db, err = storage.OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle = NewService(db, &dispatcher.NopDispatcher{})
	require.NoError(t, lifecycle.InterruptRunning(t.Context()))
	store := deploymentstore.NewStore(db)
	saved, err := store.Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, generation, saved.Verification.Generations["api"])
	assert.Contains(t, saved.Verification.UnresolvedServices, "api")
	require.ErrorIs(t, lifecycle.RecordUpdateGeneration(t.Context(), attempt.ID, "api", generation), deploymentstore.ErrNotRunning)
	desired.Compose.Services[1].Image = "worker:2"
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "two", desired, true)
	require.NoError(t, err)
	saved, err = store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, generation, saved.Verification.Generations["api"])
	require.NoError(t, lifecycle.Complete(t.Context(), next.ID, Completion{Outcome: CompletionConvergenceFailed, Verification: deploymentmodel.Verification{Known: true, UnresolvedServices: []string{"worker"}}}))
	saved, err = store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Empty(t, saved.Verification.Generations, "verified services release their generation boundary")
}
