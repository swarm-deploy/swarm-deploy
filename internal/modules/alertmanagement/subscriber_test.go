package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func newTestSubscriber(store modelstore.Store) *Subscriber {
	return newSubscriber(store, newLifecycle(store, &dispatcher.NopDispatcher{}, noAlertRecipients), &config.Config{})
}

func TestSubscriberDeploymentLifecycle(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))

	subscriber := newTestSubscriber(store)
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	sequence := 0
	subscriber.lifecycle.(*alertLifecycle).now = func() time.Time { now = now.Add(time.Minute); return now }
	subscriber.lifecycle.(*alertLifecycle).newID = func() string { sequence++; return fmt.Sprintf("id-%d", sequence) }

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "success-0", Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "api"}},
	}), "success without alert")
	alerts, err := store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err, "list empty alerts")
	assert.Empty(t, alerts, "success must be a no-op")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-1", Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{DeploymentID: "deployment-1", StackName: "api", Commit: "sha"},
		Error:       errors.New("image missing"), ErrorCode: "apply_failed", Stage: "apply",
	}}), "first failure")
	alerts, err = store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err, "list open alerts")
	require.Len(t, alerts, 1, "one incident should be created")
	firstID := alerts[0].ID
	openedAt := alerts[0].OpenedAt
	assert.Equal(t, model.AlertStatusOpen, alerts[0].Status, "alert should be open")
	assert.Equal(t, model.AlertKindSyncFailed, alerts[0].Kind)
	assert.Equal(t, "sync_failed:stack:api", alerts[0].Fingerprint)
	assert.Equal(t, uint64(1), alerts[0].Occurrences, "first occurrence")
	assert.Equal(t, "image missing", alerts[0].Message, "failure detail")
	assert.Equal(t, "apply", alerts[0].FailureStage)
	assert.Equal(t, "apply_failed", alerts[0].ErrorCode)
	assert.Equal(t, "deployment-1", alerts[0].DeploymentID)
	assert.Equal(t, "sha", alerts[0].Commit)
	assert.Equal(t, "deployment-1", alertSnapshot(alerts[0]).DeploymentID)
	assert.Equal(t, alerts[0].OpenEventID, alerts[0].LatestEventID, "first event references")
	assert.Equal(t, "failure-1", alerts[0].OpenEventID, "triggering event id")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-2", Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api"},
		Error:       errors.New("registry unavailable"),
	}}), "repeated failure")
	alerts, err = store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err, "list updated alert")
	require.Len(t, alerts, 1, "failure should update the incident")
	assert.Equal(t, firstID, alerts[0].ID, "incident id should be stable")
	assert.Equal(t, openedAt, alerts[0].OpenedAt, "repeated failures must preserve the opening time")
	assert.Equal(t, uint64(2), alerts[0].Occurrences, "occurrences should increment")
	assert.Equal(t, "registry unavailable", alerts[0].Message, "latest failure should replace the message")
	assert.Empty(t, alerts[0].DeploymentID, "the new failure has no deployment ID")
	assert.NotEqual(t, alerts[0].OpenEventID, alerts[0].LatestEventID, "latest event should advance")
	assert.Equal(t, "failure-2", alerts[0].LatestEventID, "latest event reference")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "success-1", Event: &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api"},
	}}), "recovery")
	resolved, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusResolved})
	require.NoError(t, err, "list resolved alerts")
	require.Len(t, resolved, 1, "incident should be resolved")
	assert.Equal(t, openedAt, resolved[0].OpenedAt, "closing an incident must preserve the opening time")
	assert.Equal(t, model.ResolutionReasonRecovered, resolved[0].Resolution.Reason, "resolution reason")
	assert.Equal(t, "Synchronization completed successfully", resolved[0].Resolution.Message, "resolution message")
	assert.Equal(t, "success-1", resolved[0].Resolution.EventID, "recovery event reference")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-3", Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api"}, Error: errors.New("timeout"),
	}}), "later failure")
	open, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err, "list reopened problem")
	require.Len(t, open, 1, "new incident should be open")
	assert.NotEqual(t, firstID, open[0].ID, "new incident should have a new id")
	assert.Equal(t, resolved[0].Fingerprint, open[0].Fingerprint, "problem fingerprint should be stable")

}

func TestSubscriberServiceDriftLifecycle(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	subscriber := newTestSubscriber(store)

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "repaired", Event: &events.ServiceMissed{
		StackName: "payments", ServiceName: "api", Commit: "one", Resolved: true,
	}}))
	alerts, err := store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, alerts, "same-reconciliation repair must not create a transient alert")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "missing", Event: &events.ServiceMissed{
		StackName: "payments", ServiceName: "api", Commit: "two",
	}}))
	original, err := store.FindOpenByFingerprint(ctx, model.ServiceDriftFingerprint("payments", "api"))
	require.NoError(t, err)
	original.Title = "Existing incident title"
	require.NoError(t, store.Update(ctx, original))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "drifted-again", Event: &events.ServiceDrifted{
		StackName: "payments", ServiceName: "api", Commit: "three", Reason: "replica count differs",
	}}))
	alerts, err = store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertKindServiceDrift, alerts[0].Kind)
	assert.Equal(t, model.ResourceTypeService, alerts[0].ResourceType)
	assert.Equal(t, "payments/api", alerts[0].ResourceID)
	assert.Equal(t, "service_drift:service:payments/api", alerts[0].Fingerprint)
	assert.Equal(t, uint64(2), alerts[0].Occurrences)
	assert.Equal(t, "replica count differs", alerts[0].Message)
	assert.Equal(t, "three", alerts[0].Commit)
	assert.Equal(t, original.Title, alerts[0].Title)

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "fixed", Event: &events.ServiceDriftFixed{
		StackName: "payments", ServiceName: "api", Commit: "four",
	}}))
	alerts, err = store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
	assert.Equal(t, model.ResolutionReasonRecovered, alerts[0].Resolution.Reason)
}

func TestSubscriberMasksDiagnosticsAndPreservesUsefulDetailOnGenericRepeat(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	subscriber := newTestSubscriber(store)
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "detailed", Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{DeploymentID: "one", StackName: "api"},
		Error:       errors.New("update service api: token=private-value image not found"), ErrorCode: "apply_failed", Stage: "apply",
	}}))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "generic", Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{DeploymentID: "two", StackName: "api"},
		Error:       errors.New("apply_failed"), ErrorCode: "apply_failed", Stage: "apply",
	}}))
	alerts, err := store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.EqualValues(t, 2, alerts[0].Occurrences)
	assert.Contains(t, alerts[0].Message, "image not found")
	assert.Contains(t, alerts[0].Message, "token=****")
	assert.NotContains(t, alerts[0].Message, "private-value")
	assert.Equal(t, "two", alerts[0].DeploymentID, "latest correlation should still advance")
}

func TestSubscriberConcurrentFailuresCreateOneOpenAlert(t *testing.T) {
	ctx := context.Background()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	subscriber := newTestSubscriber(store)

	const workers = 20
	var wg sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsChannel <- subscriber.Handle(ctx, events.Envelope{
				ID: "failure", Event: &events.DeployFailed{
					DeployEvent: events.DeployEvent{StackName: "api"}, Error: errors.New("failed"),
				},
			})
		}()
	}
	wg.Wait()
	close(errorsChannel)
	for handleErr := range errorsChannel {
		require.NoError(t, handleErr, "handle concurrent failure")
	}

	alerts, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err, "list alerts")
	require.Len(t, alerts, 1, "only one open incident is allowed")
	assert.Equal(t, uint64(workers), alerts[0].Occurrences, "all failures should be counted")
}

type temporaryError struct{ message string }

func (e temporaryError) Error() string   { return e.message }
func (e temporaryError) Temporary() bool { return true }

func TestSubscriberTemporaryFailureThresholdAndRecovery(t *testing.T) {
	ctx := t.Context()
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	subscriber := newTestSubscriber(store)
	stack := events.DeployEvent{StackName: "app"}
	fingerprint := model.SyncFailedFingerprint(stack.StackName)
	failure := &events.DeployFailed{DeployEvent: stack, Error: temporaryError{message: "registry unavailable"}}
	success := &events.DeploySuccess{DeployEvent: stack}

	for _, event := range []events.Envelope{
		{ID: "partial-1", Event: failure},
		{ID: "partial-2", Event: failure},
		{ID: "partial-recovery", Event: success},
		{ID: "failure-1", Event: failure},
		{ID: "failure-2", Event: failure},
	} {
		require.NoError(t, subscriber.Handle(ctx, event))
		_, err := store.FindOpenByFingerprint(ctx, fingerprint)
		require.ErrorIs(t, err, modelstore.ErrAlertNotFound, event.ID)
	}
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-3", Event: failure}))
	opened, err := store.FindOpenByFingerprint(ctx, fingerprint)
	require.NoError(t, err)
	assert.EqualValues(t, 3, opened.Occurrences)
	assert.Equal(t, "failure-3", opened.OpenEventID)

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-4", Event: failure}))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "recovery", Event: success}))
	resolved, err := store.Get(ctx, opened.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AlertStatusResolved, resolved.Status)
	assert.EqualValues(t, 4, resolved.Occurrences)
	assert.Equal(t, "failure-4", resolved.LatestEventID)
	require.NotNil(t, resolved.Resolution)
	assert.Equal(t, "recovery", resolved.Resolution.EventID)
}
