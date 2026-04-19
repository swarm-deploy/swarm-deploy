package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
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

type fakeNodeStore struct {
	nodes []inspector.NodeInfo
}

func (f *fakeNodeStore) List() []inspector.NodeInfo {
	out := make([]inspector.NodeInfo, len(f.nodes))
	copy(out, f.nodes)

	return out
}

type fakeNetworkInspector struct {
	networks []inspector.NetworkInfo
}

func (f *fakeNetworkInspector) InspectNetworks(
	_ context.Context,
) ([]inspector.NetworkInfo, error) {
	out := make([]inspector.NetworkInfo, len(f.networks))
	copy(out, f.networks)

	return out, nil
}

type fakePluginInspector struct {
	plugins []inspector.PluginInfo
}

func (f *fakePluginInspector) InspectPlugins(
	_ context.Context,
) ([]inspector.PluginInfo, error) {
	out := make([]inspector.PluginInfo, len(f.plugins))
	copy(out, f.plugins)

	return out, nil
}

type fakeSecretInspector struct {
	secrets []swarm.Secret
}

func (f *fakeSecretInspector) List(
	_ context.Context,
) ([]swarm.Secret, error) {
	out := make([]swarm.Secret, len(f.secrets))
	copy(out, f.secrets)

	return out, nil
}

type fakeServiceLogsInspector struct {
	logs []string
}

func (f *fakeServiceLogsInspector) InspectServiceLogs(
	_ context.Context,
	_ string,
	_ string,
	_ inspector.ServiceLogsOptions,
) ([]string, error) {
	out := make([]string, len(f.logs))
	copy(out, f.logs)

	return out, nil
}

type fakeServiceSpecInspector struct {
	service inspector.Service
}

func (f *fakeServiceSpecInspector) InspectServiceSpec(
	_ context.Context,
	_ string,
	_ string,
) (inspector.Service, error) {
	return f.service, nil
}

type fakeServiceStore struct {
	services []service.Info
}

func (f *fakeServiceStore) List() []service.Info {
	out := make([]service.Info, len(f.services))
	copy(out, f.services)

	return out
}

type fakeServiceReplicasManager struct{}

func (f *fakeServiceReplicasManager) GetReplicas(
	_ context.Context,
	_ swarm.ServiceReference,
) (uint64, error) {
	return 1, nil
}

func (f *fakeServiceReplicasManager) Scale(
	_ context.Context,
	_ swarm.ServiceReference,
	_ uint64,
) error {
	return nil
}

func (f *fakeServiceReplicasManager) Restart(
	_ context.Context,
	_ swarm.ServiceReference,
) (uint64, error) {
	return 1, nil
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
		&fakeNetworkInspector{},
		&fakePluginInspector{},
		&fakeSecretInspector{},
		&fakeServiceLogsInspector{},
		&fakeServiceSpecInspector{},
		&fakeServiceStore{},
		&fakeServiceReplicasManager{},
		&fakeImageVersionResolver{},
		&fakeGitRepository{},
		[]config.StackSpec{},
		&fakeCommitDiffer{},
		nil,
		&dispatcher.NopDispatcher{},
		metrics.NewGroup(metrics.CreateGroupParams{
			Namespace: "test",
			MCP:       true,
		}).MCP,
	)

	_, err := executor.Execute(context.Background(), routing.Request{
		ToolName: "unknown_tool",
		Payload:  nil,
	})
	require.Error(t, err, "expected unknown tool error")
	assert.Contains(t, err.Error(), "unknown tool", "unexpected error")
}

func TestExecutorDefinitionsContainDate(t *testing.T) {
	executor := NewExecutor(
		&fakeHistoryStore{},
		&fakeNodeStore{},
		&fakeNetworkInspector{},
		&fakePluginInspector{},
		&fakeSecretInspector{},
		&fakeServiceLogsInspector{},
		&fakeServiceSpecInspector{},
		&fakeServiceStore{},
		&fakeServiceReplicasManager{},
		&fakeImageVersionResolver{},
		&fakeGitRepository{},
		[]config.StackSpec{},
		&fakeCommitDiffer{},
		nil,
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
	assert.Contains(t, toolNames, "docker_network_list", "expected docker_network_list tool definition")
	assert.Contains(t, toolNames, "docker_plugin_list", "expected docker_plugin_list tool definition")
	assert.Contains(t, toolNames, "docker_secret_list", "expected docker_secret_list tool definition")
	assert.Contains(t, toolNames, "service_logs_get", "expected service_logs_get tool definition")
	assert.Contains(t, toolNames, "service_spec_get", "expected service_spec_get tool definition")
	assert.Contains(t, toolNames, "dns_name_resolve", "expected dns_name_resolve tool definition")
	assert.Contains(t, toolNames, "service_replicas_set", "expected service_replicas_set tool definition")
	assert.Contains(t, toolNames, "service_restart_trigger", "expected service_restart_trigger tool definition")
}
