package gitops

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/event"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/differ"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Module struct {
	Controller    *controller.Controller
	Store         *modelstore.WarmupStore
	GitRepository git.Repository
	Differ        *differ.Differ

	cfg        *config.Config
	filesystem fs.FileSystem
}

type Container interface {
	GetFileSystem() fs.FileSystem
	GetSwarm() *swarm.Swarm
	GetDeployer() deployer.StackDeployer
	GetMetrics() *metrics.Group
	GetEventModule() *event.Module
}

func InitModule(
	ctx context.Context,
	cfg *config.Config,
	cnt Container,
) (*Module, error) {
	srv := &Module{
		cfg:           cfg,
		filesystem:    cnt.GetFileSystem(),
		GitRepository: git.NewRepository(cfg.Spec.Git, filepath.Join(cfg.Spec.DataDir, "repo")),
		Differ:        differ.New(),
	}

	if err := srv.initStore(ctx); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	srv.Controller = controller.New(
		cfg,
		srv.GitRepository,
		cnt.GetSwarm(),
		cnt.GetDeployer(),
		cnt.GetMetrics(),
		cnt.GetEventModule().Dispatcher,
		srv.Store,
		cnt.GetFileSystem(),
	)

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
