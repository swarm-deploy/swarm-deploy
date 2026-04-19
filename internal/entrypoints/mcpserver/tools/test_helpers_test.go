package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
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
	secrets []swarm.Secret
	err     error
	called  int
}

func (f *fakeSecretInspector) List(_ context.Context) ([]swarm.Secret, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}

	out := make([]swarm.Secret, len(f.secrets))
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
	updateErrOnCall   int
	restartErr        error

	inspectCalled int
	updateCalled  int
	restartCalled int

	inspectedStack   string
	inspectedService string

	updatedStack    string
	updatedService  string
	updatedReplicas uint64
	updatedHistory  []uint64

	restartedStack   string
	restartedService string
}

func (f *fakeServiceReplicasManager) GetReplicas(
	_ context.Context,
	serviceRef swarm.ServiceReference,
) (uint64, error) {
	f.inspectCalled++
	f.inspectedStack = serviceRef.StackName()
	f.inspectedService = serviceRef.ServiceName()

	if f.inspectErr != nil {
		return 0, f.inspectErr
	}

	if f.replicasByService == nil {
		return 0, nil
	}

	return f.replicasByService[serviceRef.Name()], nil
}

func (f *fakeServiceReplicasManager) Scale(
	_ context.Context,
	serviceRef swarm.ServiceReference,
	replicas uint64,
) error {
	f.updateCalled++
	f.updatedStack = serviceRef.StackName()
	f.updatedService = serviceRef.ServiceName()
	f.updatedReplicas = replicas
	f.updatedHistory = append(f.updatedHistory, replicas)

	if f.updateErr != nil && (f.updateErrOnCall == 0 || f.updateCalled == f.updateErrOnCall) {
		return f.updateErr
	}

	if f.replicasByService == nil {
		f.replicasByService = map[string]uint64{}
	}
	f.replicasByService[serviceRef.Name()] = replicas

	return nil
}

func (f *fakeServiceReplicasManager) Restart(ctx context.Context, serviceRef swarm.ServiceReference) (uint64, error) {
	f.restartCalled++
	f.restartedStack = serviceRef.StackName()
	f.restartedService = serviceRef.ServiceName()

	if f.restartErr != nil {
		return 0, f.restartErr
	}

	currentReplicas, err := f.GetReplicas(ctx, serviceRef)
	if err != nil {
		return 0, fmt.Errorf("inspect service replicas: %w", err)
	}

	err = f.Scale(ctx, serviceRef, 0)
	if err != nil {
		return 0, fmt.Errorf("scale service replicas to 0: %w", err)
	}

	err = f.Scale(ctx, serviceRef, currentReplicas)
	if err != nil {
		return 0, fmt.Errorf("restore service replicas to %d: %w", currentReplicas, err)
	}

	return currentReplicas, nil
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
