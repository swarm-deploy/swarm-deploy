package configstore

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"go.uber.org/mock/gomock"
)

func TestReloadPublishesOnlyForChanges(t *testing.T) {
	dir, cfg := configForStoreTest(t)
	eventsDispatcher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	eventsDispatcher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ConfigurationChanged{})).Return(nil)
	store := New(cfg, eventsDispatcher)

	require.NoError(t, store.Reload(t.Context(), dir))
	require.NoError(t, store.Reload(t.Context(), dir))
}

func TestReloadPublishesOnceWhenPreloadedConfigurationIsUnchanged(t *testing.T) {
	dir, cfg := configForStoreTest(t)
	_, err := cfg.Reload(dir)
	require.NoError(t, err)
	eventsDispatcher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	eventsDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	store := New(cfg, eventsDispatcher)

	require.NoError(t, store.Reload(t.Context(), dir))
	require.NoError(t, store.Reload(t.Context(), dir))
}

func TestReloadWaitsForSuccessfulInitialConfigurationBeforePublishing(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Spec: config.Spec{
		StacksSource: config.StacksSourceSpec{File: "stacks.yaml"},
	}}
	eventsDispatcher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	eventsDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	store := New(cfg, eventsDispatcher)

	require.Error(t, store.Reload(t.Context(), dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte("stacks: []\n"), 0o600))
	require.NoError(t, store.Reload(t.Context(), dir))
}

func TestReloadDoesNotHoldConfigurationLockDuringPublication(t *testing.T) {
	dir, cfg := configForStoreTest(t)
	eventsDispatcher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	var store *ConfigStore
	eventsDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, events.Event) error {
			assert.Equal(t, "app", store.Stacks()[0].Name)
			return nil
		},
	)
	store = New(cfg, eventsDispatcher)
	done := make(chan error, 1)
	go func() { done <- store.Reload(t.Context(), dir) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("configuration reload deadlocked while publishing")
	}
}

func TestReloadLogsPublicationFailureAndReturnsSuccess(t *testing.T) {
	dir, cfg := configForStoreTest(t)
	eventsDispatcher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	eventsDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(errors.New("outbox unavailable"))
	store := New(cfg, eventsDispatcher)

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	require.NoError(t, store.Reload(t.Context(), dir))
	require.NoError(t, store.Reload(t.Context(), dir))
	assert.Contains(t, logs.String(), "publish configuration changed event")
	assert.Contains(t, logs.String(), "outbox unavailable")
}

func configForStoreTest(t *testing.T) (string, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(`
stacks:
  - name: app
    composeFile: app.yaml
`), 0o600))
	return dir, &config.Config{Spec: config.Spec{
		StacksSource: config.StacksSourceSpec{File: "stacks.yaml"},
	}}
}
