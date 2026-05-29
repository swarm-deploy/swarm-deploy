package tools

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/differ"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type fakeSyncControl struct {
	queued bool
	called int
}

func (f *fakeSyncControl) Manual(_ context.Context) bool {
	f.called++

	return f.queued
}

type fakeNodeStore struct {
	nodes []swarm.Node
}

func (f *fakeNodeStore) List() []swarm.Node {
	out := make([]swarm.Node, len(f.nodes))
	copy(out, f.nodes)

	return out
}

type fakeNetworkReader struct {
	networks []swarm.Network
	err      error
	called   int
}

func (f *fakeNetworkReader) List(_ context.Context) ([]swarm.Network, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}

	out := make([]swarm.Network, len(f.networks))
	copy(out, f.networks)

	return out, nil
}

type fakePluginReader struct {
	plugins []swarm.Plugin
	err     error
	called  int
}

func (f *fakePluginReader) List(_ context.Context) ([]swarm.Plugin, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}

	out := make([]swarm.Plugin, len(f.plugins))
	copy(out, f.plugins)

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
