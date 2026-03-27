package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
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

type fakeGitRepository struct{}

func (f *fakeGitRepository) List(_ context.Context, _ int) ([]gitx.CommitMeta, error) {
	return []gitx.CommitMeta{}, nil
}

func (f *fakeGitRepository) Show(_ context.Context, _ string) (gitx.Commit, error) {
	return gitx.Commit{
		Author: "test",
		Time:   time.Date(2026, time.March, 27, 0, 0, 0, 0, time.UTC),
	}, nil
}

type fakeCommitDiffer struct{}

func (f *fakeCommitDiffer) Compare(_ []differ.ComposeFile) (differ.Diff, error) {
	return differ.Diff{}, nil
}

func TestExecutorExecuteUnknownTool(t *testing.T) {
	executor := NewExecutor(
		&fakeHistoryStore{},
		&fakeNodeStore{},
		&fakeServiceStore{},
		&fakeImageVersionResolver{},
		&fakeGitRepository{},
		[]config.StackSpec{},
		&fakeCommitDiffer{},
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

func TestExecutorDefinitionsContainDate(t *testing.T) {
	executor := NewExecutor(
		&fakeHistoryStore{},
		&fakeNodeStore{},
		&fakeServiceStore{},
		&fakeImageVersionResolver{},
		&fakeGitRepository{},
		[]config.StackSpec{},
		&fakeCommitDiffer{},
		&fakeSyncControl{},
		&dispatcher.NopDispatcher{},
		metrics.NewGroup(metrics.CreateGroupParams{
			Namespace: "test",
			MCP:       true,
		}).MCP,
	)

	toolNames := make([]string, 0, len(executor.Definitions()))
	for _, definition := range executor.Definitions() {
		toolNames = append(toolNames, definition.Name)
	}

	assert.Contains(t, toolNames, "date", "expected date tool definition")
}
