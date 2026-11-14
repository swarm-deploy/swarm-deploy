package alertmanagement

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestLifecycleFailureThresholdAndPartialReset(t *testing.T) {
	ctx := t.Context()
	db, store := openAlertStore(t)
	bus := dispatcher.NewOutboxDispatcher(outbox.New(db))
	require.NoError(t, dispatcher.SubscribeMany(bus, []events.TypeName{events.TypeNameAlertOpened, events.TypeNameAlertResolved},
		"test-alert-transitions", newTestSQLSubscriber(store)))
	lifecycle := newLifecycle(store, bus, noAlertRecipients)
	fingerprint := model.SyncFailedFingerprint("app")
	failure := model.AlertAction{
		Kind: model.RecordFailure, OpenAfter: 3, TrackFailureStreak: true,
		Alert: model.Alert{Fingerprint: fingerprint, Kind: model.AlertKindSyncFailed,
			ResourceType: model.ResourceTypeStack, ResourceID: "app"},
	}
	for range 2 {
		require.NoError(t, lifecycle.Apply(ctx, []model.AlertAction{failure}))
	}
	_, err := store.FindOpenByFingerprint(ctx, fingerprint)
	require.ErrorIs(t, err, modelstore.ErrAlertNotFound)

	// A recovery clears a partial streak even when there is no incident to resolve.
	require.NoError(t, lifecycle.Apply(ctx, []model.AlertAction{
		{Kind: model.ResetFailureStreak, Alert: model.Alert{Fingerprint: fingerprint}},
		{Kind: model.Resolve, Alert: model.Alert{
			Fingerprint: fingerprint, Resolution: &model.AlertResolution{Reason: model.ResolutionReasonRecovered},
		}},
	}))
	assert.Zero(t, alertSignalCount(t, db, "AlertResolved"))

	for i := range 3 {
		failure.Alert.LatestEventID = fmt.Sprintf("failure-%d", i+1)
		require.NoError(t, lifecycle.Apply(ctx, []model.AlertAction{failure}))
		if i < 2 {
			_, findErr := store.FindOpenByFingerprint(ctx, fingerprint)
			require.ErrorIs(t, findErr, modelstore.ErrAlertNotFound)
			assert.Zero(t, alertSignalCount(t, db, "AlertOpened"))
		}
	}
	alert, err := store.FindOpenByFingerprint(ctx, fingerprint)
	require.NoError(t, err)
	assert.EqualValues(t, 3, alert.Occurrences)
	assert.Equal(t, "failure-3", alert.OpenEventID)
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertOpened"))
}

func TestLifecycleUpdatesOpenIncidentAndAccumulatesDistinctFindings(t *testing.T) {
	ctx := t.Context()
	db, store := openAlertStore(t)
	bus := dispatcher.NewOutboxDispatcher(outbox.New(db))
	require.NoError(t, dispatcher.SubscribeMany(bus, []events.TypeName{events.TypeNameAlertOpened, events.TypeNameAlertResolved},
		"test-alert-transitions", newTestSQLSubscriber(store)))
	lifecycle := newLifecycle(store, bus, noAlertRecipients)
	start := time.Date(2026, time.November, 7, 10, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	now := start
	lifecycle.now = func() time.Time { return now }
	fingerprint := model.SyncFailedFingerprint("app")
	failure := model.AlertAction{Kind: model.RecordFailure, OpenAfter: 1, TrackFailureStreak: true,
		Alert: model.Alert{
			Fingerprint: fingerprint, Kind: model.AlertKindSyncFailed, ResourceType: model.ResourceTypeStack, ResourceID: "app",
			Message: "useful detail", LatestEventID: "failure",
		},
		Findings: []model.Finding{{Code: faults.CodeImageNotFound, Summary: "No such image: api:1"}},
	}
	require.NoError(t, lifecycle.Apply(ctx, []model.AlertAction{failure}))
	opened, err := store.FindOpenByFingerprint(ctx, fingerprint)
	require.NoError(t, err)
	assert.EqualValues(t, 1, opened.Occurrences)

	now = start.Add(time.Minute)
	failure.OpenAfter = 3
	failure.Alert = opened
	failure.Alert.DeploymentID = "new-deployment"
	failure.Alert.LatestEventID = "repeat"
	failure.Findings = append(failure.Findings,
		model.Finding{Code: faults.CodeImageNotFound, Summary: "No such image: api:2"},
		model.Finding{Code: faults.CodeImageNotFound},
	)
	require.NoError(t, lifecycle.Apply(ctx, []model.AlertAction{failure}))
	updated, err := store.FindOpenByFingerprint(ctx, fingerprint)
	require.NoError(t, err)
	assert.Equal(t, opened.ID, updated.ID)
	assert.True(t, opened.OpenedAt.Equal(updated.OpenedAt))
	assert.Equal(t, opened.OpenEventID, updated.OpenEventID)
	assert.EqualValues(t, 2, updated.Occurrences, "an open incident updates before the new threshold is met")
	assert.Equal(t, "useful detail", updated.Message)
	assert.Equal(t, "new-deployment", updated.DeploymentID)
	assert.Equal(t, "repeat", updated.LatestEventID)
	assert.Equal(t, []model.Finding{
		{Code: faults.CodeImageNotFound, Summary: "No such image: api:1", DetectedAt: start.UTC()},
		{Code: faults.CodeImageNotFound, Summary: "No such image: api:2", DetectedAt: now.UTC()},
		{Code: faults.CodeImageNotFound, DetectedAt: now.UTC()},
	}, updated.Findings)
	assert.True(t, failure.Findings[0].DetectedAt.IsZero(), "applying actions must preserve their templates")
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertOpened"))

	now = start.Add(2 * time.Minute)
	resolution := model.AlertResolution{Reason: model.ResolutionReasonRecovered, Message: "recovered", EventID: "success"}
	require.NoError(t, lifecycle.Apply(ctx, []model.AlertAction{{
		Kind: model.Resolve, Alert: model.Alert{Fingerprint: fingerprint, Resolution: &resolution},
	}}))
	resolved, err := store.Get(ctx, opened.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AlertStatusResolved, resolved.Status)
	assert.Equal(t, &resolution, resolved.Resolution)
	assert.Equal(t, updated.LatestEventID, resolved.LatestEventID)
	assert.Equal(t, updated.Findings, resolved.Findings)
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertResolved"))
}

func TestLifecycleSkipsStreaksForNodeAndDrift(t *testing.T) {
	db, store := openAlertStore(t)
	lifecycle := newLifecycle(store, &dispatcher.NopDispatcher{}, noAlertRecipients)
	for _, alert := range []model.Alert{
		{Fingerprint: model.NodeDisconnectedFingerprint("worker"), Kind: model.AlertKindNodeDisconnected,
			ResourceType: model.ResourceTypeNode, ResourceID: "worker", LatestEventID: "node"},
		{Fingerprint: model.ServiceDriftFingerprint("app", "api"), Kind: model.AlertKindServiceDrift,
			ResourceType: model.ResourceTypeService, ResourceID: "app/api", LatestEventID: "drift"},
	} {
		require.NoError(t, lifecycle.Apply(t.Context(), []model.AlertAction{{
			Kind: model.RecordFailure, OpenAfter: 1, Alert: alert,
		}}))
		opened, err := store.FindOpenByFingerprint(t.Context(), alert.Fingerprint)
		require.NoError(t, err)
		assert.EqualValues(t, 1, opened.Occurrences)
	}
	var streaks int
	require.NoError(t, db.Get(t.Context(), &streaks, "SELECT count(*) FROM alert_failure_streaks"))
	assert.Zero(t, streaks)
}
