package tools

import (
	"context"
	"time"

	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm/inspector"
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

func (f *fakeSyncControl) Manual(_ context.Context) bool {
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

type fakeNetworkInspector struct {
	networks []inspector.NetworkInfo
	err      error
	called   int
}

func (f *fakeNetworkInspector) InspectNetworks(_ context.Context) ([]inspector.NetworkInfo, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}

	out := make([]inspector.NetworkInfo, len(f.networks))
	copy(out, f.networks)

	return out, nil
}

type fakePluginInspector struct {
	plugins []inspector.PluginInfo
	err     error
	called  int
}

func (f *fakePluginInspector) InspectPlugins(_ context.Context) ([]inspector.PluginInfo, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}

	out := make([]inspector.PluginInfo, len(f.plugins))
	copy(out, f.plugins)

	return out, nil
}

type fakeSecretInspector struct {
	secrets []inspector.SecretInfo
	err     error
	called  int
}

func (f *fakeSecretInspector) InspectSecrets(_ context.Context) ([]inspector.SecretInfo, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}

	out := make([]inspector.SecretInfo, len(f.secrets))
	copy(out, f.secrets)

	return out, nil
}

type fakeServiceStore struct {
	services []service.Info
}

func (f *fakeServiceStore) List() []service.Info {
	out := make([]service.Info, len(f.services))
	copy(out, f.services)

	return out
}

type fakeServiceReplicasManager struct {
	replicasByService map[string]uint64
	inspectErr        error
	updateErr         error

	inspectCalled int
	updateCalled  int

	inspectedStack   string
	inspectedService string

	updatedStack    string
	updatedService  string
	updatedReplicas uint64
}

func (f *fakeServiceReplicasManager) InspectServiceReplicas(
	_ context.Context,
	stackName,
	serviceName string,
) (uint64, error) {
	f.inspectCalled++
	f.inspectedStack = stackName
	f.inspectedService = serviceName

	if f.inspectErr != nil {
		return 0, f.inspectErr
	}

	if f.replicasByService == nil {
		return 0, nil
	}

	return f.replicasByService[stackName+"_"+serviceName], nil
}

func (f *fakeServiceReplicasManager) UpdateServiceReplicas(
	_ context.Context,
	stackName,
	serviceName string,
	replicas uint64,
) error {
	f.updateCalled++
	f.updatedStack = stackName
	f.updatedService = serviceName
	f.updatedReplicas = replicas

	if f.updateErr != nil {
		return f.updateErr
	}

	if f.replicasByService == nil {
		f.replicasByService = map[string]uint64{}
	}
	f.replicasByService[stackName+"_"+serviceName] = replicas

	return nil
}

type fakeImageVersionResolver struct {
	version registry.ImageVersion
	err     error
	called  int
	image   string
}

func (f *fakeImageVersionResolver) ResolveActualVersion(
	_ context.Context,
	image string,
) (registry.ImageVersion, error) {
	f.called++
	f.image = image

	if f.err != nil {
		return registry.ImageVersion{}, f.err
	}

	return f.version, nil
}

type fakeGitRepository struct {
	commit  gitx.Commit
	commits []gitx.CommitMeta
	err     error

	showCalled int
	showHash   string

	listCalled int
	listLimit  int
}

func (f *fakeGitRepository) Show(_ context.Context, hash string) (gitx.Commit, error) {
	f.showCalled++
	f.showHash = hash
	if f.err != nil {
		return gitx.Commit{}, f.err
	}

	return f.commit, nil
}

func (f *fakeGitRepository) List(_ context.Context, limit int) ([]gitx.CommitMeta, error) {
	f.listCalled++
	f.listLimit = limit
	if f.err != nil {
		return nil, f.err
	}

	out := make([]gitx.CommitMeta, len(f.commits))
	copy(out, f.commits)

	return out, nil
}

type fakeCommitDiffer struct {
	diff         differ.Diff
	err          error
	called       int
	composeFiles []differ.ComposeFile
}

func (f *fakeCommitDiffer) Compare(composeFiles []differ.ComposeFile) (differ.Diff, error) {
	f.called++
	f.composeFiles = make([]differ.ComposeFile, len(composeFiles))
	copy(f.composeFiles, composeFiles)

	if f.err != nil {
		return differ.Diff{}, f.err
	}

	return f.diff, nil
}

func defaultCommitTime() time.Time {
	return time.Date(2026, time.March, 27, 0, 0, 0, 0, time.UTC)
}
