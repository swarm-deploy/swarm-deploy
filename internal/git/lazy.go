package git

import (
	"context"
	"sync"

	"github.com/artarts36/swarm-deploy/internal/config"
)

func NewLazyProxy(spec config.GitRepositorySpec, path string) *LazyProxy {
	return &LazyProxy{
		spec: spec,
		path: path,
	}
}

type LazyProxy struct {
	spec config.GitRepositorySpec
	path string

	mu         sync.Mutex
	repository *GoGitRepository
}

func (p *LazyProxy) Pull(ctx context.Context) error {
	repo, err := p.init(ctx)
	if err != nil {
		return err
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

func (p *LazyProxy) Show(ctx context.Context, commitHash string) (Commit, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return Commit{}, err
	}

	return repo.Show(ctx, commitHash)
}

func (p *LazyProxy) Branch(ctx context.Context, branch string) (Repository, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return repo, err
	}

	return repo.Branch(ctx, branch)
}

func (p *LazyProxy) Add(ctx context.Context, path string) error {
	repo, err := p.init(ctx)
	if err != nil {
		return err
	}

	return repo.Add(ctx, path)
}

func (p *LazyProxy) Commit(ctx context.Context, message string, author CommitAuthor) (string, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return "", err
	}

	return repo.Commit(ctx, message, author)
}

func (p *LazyProxy) Push(ctx context.Context, branch string) error {
	repo, err := p.init(ctx)
	if err != nil {
		return err
	}

	return repo.Push(ctx, branch)
}

func (p *LazyProxy) WorkingDir() string {
	return p.path
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
