package gitops

import (
	"context"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/differ"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Module struct {
	// Deployments queries actual apply attempts, independently of event projections.
	Deployments   *deployment.Store
	Controller    *controller.Controller
	Store         *modelstore.SQLStore
	GitRepository git.Repository
	Differ        *differ.Differ

	cfg        *config.Config
	filesystem fs.FileSystem
}

type Container interface {
	// GetStorage returns the shared database and transactor.
	GetStorage() *storage.Database
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

	srv.Store = modelstore.NewSQLStore(cnt.GetStorage())
	srv.Deployments = deployment.NewStore(cnt.GetStorage())
	if err := deployment.NewService(cnt.GetStorage(), cnt.GetEventModule().Dispatcher).InterruptRunning(ctx); err != nil {
		return nil, err
	}

	srv.Controller = controller.New(
		cfg,
		srv.GitRepository,
		cnt.GetSwarm(),
		cnt.GetDeployer(),
		cnt.GetMetrics(),
		cnt.GetEventModule().Dispatcher,
		srv.Store,
		cnt.GetFileSystem(), cnt.GetStorage(),
	)

	return srv, nil
}
