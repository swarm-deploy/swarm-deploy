package gitops

import (
	"context"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
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
	Deployments   *deploymentstore.Store
	Controller    *controller.Controller
	Store         *modelstore.SQLStore
	GitRepository git.Repository
	Differ        *differ.Differ

	cfg        *config.Config
	filesystem fs.FileSystem
}

type Container interface {
	// GetStorage returns the shared database and transactor.
	GetStorage() storage.Database
	GetFileSystem() fs.FileSystem
	GetSwarm() *swarm.Swarm
	// GetDeploymentsModule returns deployment execution infrastructure.
	GetDeploymentsModule() *deployments.Module
	GetMetrics() *metrics.Group
	GetEventModule() *event.Module
	// GetConfigStore returns the shared runtime configuration store.
	GetConfigStore() *configstore.ConfigStore
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
	srv.Deployments = deploymentstore.NewStore(cnt.GetStorage())
	projection := deployment.NewProjectionSubscriber(cnt.GetStorage(), srv.Deployments, srv.Store)
	if err := dispatcher.SubscribeMany(cnt.GetEventModule().Dispatcher, []events.TypeName{
		events.TypeNameDeploySuccess, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted,
	}, "gitops-runtime", projection); err != nil {
		return nil, err
	}

	srv.Controller = controller.New(
		cfg,
		cnt.GetConfigStore(),
		srv.GitRepository,
		cnt.GetSwarm(),
		cnt.GetDeploymentsModule().Controller,
		cnt.GetMetrics(),
		cnt.GetEventModule().Dispatcher,
		srv.Store,
		cnt.GetFileSystem(), cnt.GetStorage(),
	)
	return srv, nil
}
