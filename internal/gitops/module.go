package gitops

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

type Module struct {
	Store         *modelstore.WarmupStore
	GitRepository git.Repository

	cfg        *config.Config
	filesystem fs.FileSystem
}

type Container interface {
	GetFileSystem() fs.FileSystem
}

func InitService(
	ctx context.Context,
	cfg *config.Config,
	cnt Container,
) (*Module, error) {
	srv := &Module{
		cfg:           cfg,
		filesystem:    cnt.GetFileSystem(),
		GitRepository: git.NewRepository(cfg.Spec.Git, filepath.Join(cfg.Spec.DataDir, "repo")),
	}

	if err := srv.initStore(ctx); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	return srv, nil
}

func (s *Module) initStore(ctx context.Context) error {
	fileStore, err := modelstore.NewFileStore(ctx,
		filepath.Join(s.cfg.Spec.DataDir, "controller.state.json"),
		s.filesystem,
	)
	if err != nil {
		return fmt.Errorf("init file store: %w", err)
	}

	warmupStore := modelstore.NewWarmupStore(modelstore.NewMemoryStore(), fileStore)
	warmupStore.Warmup(ctx)

	s.Store = warmupStore

	return nil
}
