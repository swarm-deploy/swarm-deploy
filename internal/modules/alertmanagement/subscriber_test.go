package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestSubscriberDeploymentLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "alerts.json")
	store, err := modelstore.NewFileStore(ctx, path, sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "create store")

	subscriber := NewSubscriber(store)
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	sequence := 0
	subscriber.now = func() time.Time { now = now.Add(time.Minute); return now }
	subscriber.newID = func() string { sequence++; return fmt.Sprintf("id-%d", sequence) }

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "success-0", Payload: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "api"}},
	}), "success without alert")
	alerts, err := store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err, "list empty alerts")
	assert.Empty(t, alerts, "success must be a no-op")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-1", Payload: &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api"},
		Error:       errors.New("image missing"),
	}}), "first failure")
	alerts, err = store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err, "list open alerts")
	require.Len(t, alerts, 1, "one incident should be created")
	firstID := alerts[0].ID
	assert.Equal(t, model.AlertStatusOpen, alerts[0].Status, "alert should be open")
	assert.Equal(t, uint64(1), alerts[0].Occurrences, "first occurrence")
	assert.Equal(t, "image missing", alerts[0].Message, "failure detail")
	assert.Equal(t, alerts[0].OpenEventID, alerts[0].LatestEventID, "first event references")
	assert.Equal(t, "failure-1", alerts[0].OpenEventID, "triggering event id")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-2", Payload: &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api"},
		Error:       errors.New("registry unavailable"),
	}}), "repeated failure")
	alerts, err = store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err, "list updated alert")
	require.Len(t, alerts, 1, "failure should update the incident")
	assert.Equal(t, firstID, alerts[0].ID, "incident id should be stable")
	assert.Equal(t, uint64(2), alerts[0].Occurrences, "occurrences should increment")
	assert.Equal(t, "registry unavailable", alerts[0].Message, "latest failure should replace the message")
	assert.NotEqual(t, alerts[0].OpenEventID, alerts[0].LatestEventID, "latest event should advance")
	assert.Equal(t, "failure-2", alerts[0].LatestEventID, "latest event reference")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "success-1", Payload: &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api"},
	}}), "recovery")
	resolved, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusResolved})
	require.NoError(t, err, "list resolved alerts")
	require.Len(t, resolved, 1, "incident should be resolved")
	assert.Equal(t, model.ResolutionReasonRecovered, resolved[0].Resolution.Reason, "resolution reason")
	assert.Equal(t, "Deployment completed successfully", resolved[0].Resolution.Message, "resolution message")
	assert.Equal(t, "success-1", resolved[0].Resolution.EventID, "recovery event reference")

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{ID: "failure-3", Payload: &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api"}, Error: errors.New("timeout"),
	}}), "later failure")
	open, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err, "list reopened problem")
	require.Len(t, open, 1, "new incident should be open")
	assert.NotEqual(t, firstID, open[0].ID, "new incident should have a new id")
	assert.Equal(t, resolved[0].Fingerprint, open[0].Fingerprint, "problem fingerprint should be stable")

	reloaded, err := modelstore.NewFileStore(ctx, path, sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "reload persisted alerts")
	persisted, err := reloaded.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err, "list reloaded alerts")
	assert.Len(t, persisted, 2, "alerts should survive restart")
}

func TestSubscriberConcurrentFailuresCreateOneOpenAlert(t *testing.T) {
	ctx := context.Background()
	store, err := modelstore.NewFileStore(ctx, filepath.Join(t.TempDir(), "alerts.json"), sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "create store")
	subscriber := NewSubscriber(store)

	const workers = 20
	var wg sync.WaitGroup
	errorsChannel := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsChannel <- subscriber.Handle(ctx, events.Envelope{
				ID: "failure", Payload: &events.DeployFailed{
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
