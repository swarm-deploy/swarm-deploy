package git

import (
	"context"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func NewLazyProxy(spec config.GitSpec, path string) *LazyProxy {
	return &LazyProxy{
		spec: spec,
		path: path,
	}
}

type LazyProxy struct {
	spec config.GitSpec
	path string

	mu         sync.Mutex
	repository *GoGitRepository
}

func (p *LazyProxy) WorkingDir() string {
	return p.path
}

func (p *LazyProxy) ReadFile(ctx context.Context, path string) ([]byte, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return []byte{}, err
	}

	return repo.ReadFile(ctx, path)
}

func (p *LazyProxy) Pull(ctx context.Context) (PullResult, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return PullResult{}, err
	}

	return repo.Pull(ctx)
}

func (p *LazyProxy) Head(ctx context.Context) (string, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return "", err
	}

	return repo.Head(ctx)
}

func (p *LazyProxy) List(ctx context.Context, limit int) ([]CommitMeta, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return nil, err
	}

	return repo.List(ctx, limit)
}

func (p *LazyProxy) Diff(ctx context.Context, oldRevision string, newRevision string) ([]CommitFileDiff, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return nil, err
	}

	return repo.Diff(ctx, oldRevision, newRevision)
}

func (p *LazyProxy) Show(ctx context.Context, commitHash string) (Commit, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return Commit{}, err
	}

	return repo.Show(ctx, commitHash)
}

func (p *LazyProxy) init(ctx context.Context) (*GoGitRepository, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.repository != nil {
		return p.repository, nil
	}

	repo, err := NewGoGitRepository(ctx, p.spec, p.path)
	if err != nil {
		return nil, err
	}

	p.repository = repo

	return p.repository, nil
}
