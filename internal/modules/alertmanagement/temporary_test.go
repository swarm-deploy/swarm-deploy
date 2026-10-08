package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

type fault struct{ temporary bool }

func (f fault) Error() string   { return "fault" }
func (f fault) Temporary() bool { return f.temporary }

func failed(id string, err error) events.Envelope {
	return events.Envelope{ID: id, Event: &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api"}, Error: err,
	}}
}

func succeeded(id string) events.Envelope {
	return events.Envelope{ID: id, Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "api"}}}
}

func newTestSubscriber(t *testing.T) (*Subscriber, *modelstore.FileStore, *MockObserver) {
	t.Helper()
	ctx := context.Background()
	store, err := modelstore.NewFileStore(ctx, filepath.Join(t.TempDir(), "alerts.json"), sharedfs.NewLocalFileSystem())
	require.NoError(t, err, "create store")
	subscriber := NewSubscriber(store)
	observer := NewMockObserver(gomock.NewController(t))
	subscriber.Observe(observer)
	return subscriber, store, observer
}

func TestTemporaryFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		failures   int
		wantOpened bool
	}{
		{name: "permanent opens immediately", err: fault{temporary: false}, failures: 1, wantOpened: true},
		{name: "plain error opens immediately", err: errors.New("boom"), failures: 1, wantOpened: true},
		{name: "temporary below threshold", err: fault{temporary: true}, failures: 2, wantOpened: false},
		{name: "temporary at threshold", err: fault{temporary: true}, failures: 3, wantOpened: true},
		{
			name:       "wrapped temporary below threshold",
			err:        fmt.Errorf("deploy: %w", fmt.Errorf("pull: %w", fault{temporary: true})),
			failures:   2,
			wantOpened: false,
		},
		{
			name:       "wrapped temporary at threshold",
			err:        fmt.Errorf("deploy: %w", fault{temporary: true}),
			failures:   3,
			wantOpened: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			subscriber, store, observer := newTestSubscriber(t)
			if tt.wantOpened {
				observer.EXPECT().AlertOpened(gomock.Any(), gomock.Any()).Return(nil)
			}

			for i := range tt.failures {
				require.NoError(t, subscriber.Handle(ctx, failed(fmt.Sprintf("f-%d", i), tt.err)), "handle failure")
			}

			alerts, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
			require.NoError(t, err, "list alerts")
			assert.Equal(t, tt.wantOpened, len(alerts) == 1, "alert opened")
		})
	}
}

func TestSuccessResetsTemporaryWait(t *testing.T) {
	ctx := context.Background()
	subscriber, store, _ := newTestSubscriber(t)
	temp := fault{temporary: true}

	for _, ev := range []events.Envelope{
		failed("1", temp), failed("2", temp), succeeded("3"), failed("4", temp), failed("5", temp),
	} {
		require.NoError(t, subscriber.Handle(ctx, ev), "handle event")
	}

	alerts, err := store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err, "list alerts")
	assert.Empty(t, alerts, "success must reset temporary failure counter")
}

func TestObserverSeesLifecycleAndReopening(t *testing.T) {
	ctx := context.Background()
	subscriber, _, observer := newTestSubscriber(t)

	var opened, resolved []model.Alert
	observer.EXPECT().AlertOpened(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a model.Alert) error { opened = append(opened, a); return nil }).Times(2)
	observer.EXPECT().AlertResolved(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a model.Alert) error { resolved = append(resolved, a); return nil }).Times(2)

	for _, ev := range []events.Envelope{
		failed("1", errors.New("x")), failed("2", errors.New("x")), succeeded("3"),
		failed("4", errors.New("x")), succeeded("5"),
	} {
		require.NoError(t, subscriber.Handle(ctx, ev), "handle event")
	}

	require.Len(t, opened, 2, "update must not notify")
	require.Len(t, resolved, 2, "resolutions")
	assert.NotEqual(t, opened[0].ID, opened[1].ID, "reopened problem gets a new alert")
	assert.Equal(t, opened[0].ID, resolved[0].ID, "resolved alert matches opened")
	assert.Equal(t, "1", resolved[0].OpenEventID, "open event")
	assert.Equal(t, "2", resolved[0].LatestEventID, "latest event")
	assert.Equal(t, "3", resolved[0].Resolution.EventID, "resolution event")
}

func TestObserverFailureDoesNotAffectState(t *testing.T) {
	ctx := context.Background()
	subscriber, store, observer := newTestSubscriber(t)
	observer.EXPECT().AlertOpened(gomock.Any(), gomock.Any()).Return(errors.New("telegram down"))
	observer.EXPECT().AlertResolved(gomock.Any(), gomock.Any()).Return(errors.New("telegram down"))

	require.NoError(t, subscriber.Handle(ctx, failed("1", errors.New("x"))), "failure must not fail on observer error")
	require.NoError(t, subscriber.Handle(ctx, succeeded("2")), "success must not fail on observer error")

	resolved, err := store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusResolved})
	require.NoError(t, err, "list alerts")
	assert.Len(t, resolved, 1, "alert state is persisted regardless of notifications")
}
