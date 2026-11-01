package deployments

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func TestTerminalFailureSuppressesTicksAcrossRestartButAllowsManualAndChangedDesired(t *testing.T) {
	db, service, bus := openService(t)
	subscribe(t, db, bus, events.TypeNameDeployFailed)
	desired := desiredServices("api")
	first, err := service.StartIfChanged(t.Context(), "app", "first", desired, true)
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), first.ID, Completion{Outcome: CompletionConvergenceFailed}))
	for range 10 {
		// Recreating the lifecycle service simulates losing all process-local retry state.
		service = NewService(db, bus, WithClock(func() time.Time { return time.Now().Add(24 * time.Hour) }))
		skipped, err := service.StartIfChanged(t.Context(), "app", "unrelated-commit", desired, true)
		require.NoError(t, err)
		assert.Nil(t, skipped)
	}
	stored, err := deploymentstore.NewStore(db).Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, 1, stored.AttemptCount)
	assert.Len(t, publications(t, db, events.TypeNameDeployFailed), 1)
	retry, err := service.StartIfChanged(t.Context(), "app", "retry", desired, false, StartOptions{ManualRetry: true})
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, first.DeploymentID, retry.DeploymentID)
	assert.Equal(t, 2, retry.Number)
	require.NoError(t, service.Complete(t.Context(), retry.ID, Completion{Outcome: CompletionConvergenceFailed}))
	desired.Compose.Services[0].Image = "api:2"
	changed, err := service.StartIfChanged(t.Context(), "app", "changed", desired, false)
	require.NoError(t, err)
	require.NotNil(t, changed)
	assert.NotEqual(t, first.DeploymentID, changed.DeploymentID)
	assert.Equal(t, 1, changed.Number)
}

func TestAutomaticRetryBackoffAndBudgetArePersisted(t *testing.T) {
	for _, outcome := range []CompletionOutcome{CompletionApplyFailed, CompletionInitJobFailed, CompletionVerificationInterrupted, CompletionProcessInterrupted, CompletionCleanupFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			now := time.Now().UTC()
			clock := func() time.Time { return now }
			db, service, bus := openService(t, WithClock(clock))
			desired := desiredServices("api")
			attempt, err := service.StartIfChanged(t.Context(), "app", "first", desired, true)
			require.NoError(t, err)
			for number := 1; number <= maxAutomaticAttempts; number++ {
				require.NotNil(t, attempt)
				assert.Equal(t, number, attempt.Number)
				completion := Completion{Outcome: outcome}
				if outcome == CompletionCleanupFailed {
					completion.ObservedAt = &now
				}
				require.NoError(t, service.Complete(t.Context(), attempt.ID, completion))
				service = NewService(db, bus, WithClock(clock))
				for range 3 {
					skipped, err := service.StartIfChanged(t.Context(), "app", "tick", desired, false)
					require.NoError(t, err)
					assert.Nil(t, skipped)
				}
				now = now.Add(automaticRetryDelay * time.Duration(1<<(number-1)))
				attempt, err = service.StartIfChanged(t.Context(), "app", "tick", desired, false)
				require.NoError(t, err)
			}
			assert.Nil(t, attempt, "automatic retries must stop even after restarting the lifecycle service")
			manual, err := service.StartIfChanged(t.Context(), "app", "manual", desired, false, StartOptions{ManualRetry: true})
			require.NoError(t, err)
			require.NotNil(t, manual)
			assert.Equal(t, maxAutomaticAttempts+1, manual.Number)
			require.NoError(t, service.Complete(t.Context(), manual.ID, Completion{Outcome: CompletionSucceeded}))
			stored, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Succeeded, stored.Status)
		})
	}
}
