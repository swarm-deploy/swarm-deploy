package git

import (
	"context"
	"sync"

	"github.com/artarts36/swarm-deploy/internal/config"
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
