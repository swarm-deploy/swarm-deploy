package mcpserver

import (
	"context"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHistoryStore struct {
	entries []history.Entry
}

func (f *fakeHistoryStore) List() []history.Entry {
	out := make([]history.Entry, len(f.entries))
	copy(out, f.entries)

	return out
}

type fakeSyncControl struct {
	queued bool
	called int
}

func (f *fakeSyncControl) Trigger(_ controller.TriggerReason) bool {
	f.called++

	return f.queued
}

type fakeNodeStore struct {
	nodes []inspector.NodeInfo
}

func (f *fakeNodeStore) List() []inspector.NodeInfo {
	out := make([]inspector.NodeInfo, len(f.nodes))
	copy(out, f.nodes)

	return out
}

type fakeServiceStore struct {
	services []service.Info
}

func (f *fakeServiceStore) List() []service.Info {
	out := make([]service.Info, len(f.services))
	copy(out, f.services)

	return out
}

type fakeImageVersionResolver struct{}

func (f *fakeImageVersionResolver) ResolveActualVersion(
	_ context.Context,
	_ string,
) (registry.ImageVersion, error) {
	return registry.ImageVersion{}, nil
}

func TestExecutorExecuteUnknownTool(t *testing.T) {
	executor := NewExecutor(
		&fakeHistoryStore{},
		&fakeNodeStore{},
		&fakeServiceStore{},
		&fakeImageVersionResolver{},
		&fakeSyncControl{},
		&dispatcher.NopDispatcher{},
		metrics.NewGroup(metrics.CreateGroupParams{
			Namespace: "test",
			MCP:       true,
		}).MCP,
	)

	_, err := executor.Execute(context.Background(), "unknown_tool", nil)
	require.Error(t, err, "expected unknown tool error")
	assert.Contains(t, err.Error(), "unknown tool", "unexpected error")
}
