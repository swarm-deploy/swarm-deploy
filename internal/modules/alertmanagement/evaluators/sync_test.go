package evaluators

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestSyncEvaluatorKeepsThresholdAcrossTemporaryFailures(t *testing.T) {
	evaluator := NewSyncEvaluator(modelstore.NewSQLStore(testutil.OpenSQLite(t)), &config.Config{})
	for i, event := range []events.Event{
		&events.DeployFailed{
			DeployEvent: events.DeployEvent{StackName: "app"},
			Error:       faults.NewSafeError("temporary deploy failure", true, "", ""),
		},
		&events.DeployPreparationFailed{
			StackName: "app", Error: faults.NewSafeError("temporary preparation failure", true, "", ""),
		},
		&events.DeployInterrupted{
			StackName: "app", Error: faults.NewSafeError("temporary interruption", true, "", ""),
		},
	} {
		id := fmt.Sprintf("failure-%d", i)
		actions, err := evaluator.Evaluate(t.Context(), events.Envelope{ID: id, Event: event})
		require.NoError(t, err)
		require.Len(t, actions, 1)
		assert.Equal(t, model.AlertActionRecordFailure, actions[0].Kind)
		assert.EqualValues(t, 3, actions[0].OpenAfter)
		assert.True(t, actions[0].TrackFailureStreak)
		assert.Equal(t, model.SyncFailedFingerprint("app"), actions[0].Alert.Fingerprint)
		assert.Equal(t, id, actions[0].Alert.LatestEventID)
		assert.Empty(t, actions[0].Alert.ID)
		assert.Zero(t, actions[0].Alert.Occurrences)
		assert.True(t, actions[0].Alert.OpenedAt.IsZero())
	}
}

func TestSyncEvaluatorFailureDiagnostics(t *testing.T) {
	evaluator := NewSyncEvaluator(modelstore.NewSQLStore(testutil.OpenSQLite(t)), &config.Config{})
	for _, tc := range []struct {
		name    string
		event   events.Event
		title   string
		message string
		stage   string
		code    string
	}{
		{
			name:  "preparation uses the known stage",
			event: &events.DeployPreparationFailed{StackName: "app", ErrorCode: "preparation_failed", Stage: "validation"},
			title: "Synchronization failed", stage: "validation", code: "preparation_failed",
			message: "Synchronization failed during validation. No detailed diagnostic was recorded.",
		},
		{
			name:  "interruption keeps the outcome unknown",
			event: &events.DeployInterrupted{StackName: "app", Reason: "process_interrupted"},
			title: "Synchronization outcome unknown", code: "process_interrupted",
			message: "Deployment outcome is unknown after interruption during an unknown stage. No detailed diagnostic was recorded.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions, err := evaluator.Evaluate(t.Context(), events.Envelope{ID: "source", Event: tc.event})
			require.NoError(t, err)
			require.Len(t, actions, 1)
			action := actions[0]
			assert.EqualValues(t, 1, action.OpenAfter)
			assert.Equal(t, tc.title, action.Alert.Title)
			assert.Equal(t, tc.message, action.Alert.Message)
			assert.Equal(t, tc.stage, action.Alert.FailureStage)
			assert.Equal(t, tc.code, action.Alert.ErrorCode)
			assert.Empty(t, action.Alert.DeploymentID)
		})
	}
}
