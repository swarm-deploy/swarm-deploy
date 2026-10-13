package configstore

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
