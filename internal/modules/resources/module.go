package resources

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Module struct {
	NodeStore     node.Repository
	NodeCollector *node.Collector
	// ServiceStore persists and queries service metadata snapshots.
	ServiceStore modelstore.Store
	// Secrets exposes persisted secret metadata and its collector.
	Secrets *secrets.Domain
	// SecretManagers discovers and controls Secret Manager services.
	SecretManagers *secretmanager.Domain

	cfg *config.Config
}

type Container interface {
	// GetStorage returns the shared database.
	GetStorage() *storage.Database
	GetSwarm() *swarm.Swarm
	GetEventModule() *event.Module
	GetFileSystem() fs.FileSystem
}

func InitModule(
	ctx context.Context,
	cfg *config.Config,
	cnt Container,
) (*Module, error) {
	srv := &Module{
		cfg: cfg,
	}

	srv.NodeStore = node.NewSQLStore(cnt.GetStorage())
	srv.ServiceStore = modelstore.NewSQLStore(cnt.GetStorage())

	srv.NodeCollector = node.NewNodeCollector(cnt.GetSwarm().Nodes, srv.NodeStore, cnt.GetEventModule().Dispatcher)
	secretDomain, err := secrets.NewDomain(
		cnt.GetStorage(),
		cnt.GetSwarm().Secrets,
	)
	if err != nil {
		return nil, fmt.Errorf("init secrets domain: %w", err)
	}
	srv.Secrets = secretDomain
	srv.SecretManagers = secretmanager.NewDomain(srv.ServiceStore)

	srv.registerEventSubscribers(cnt)

	return srv, nil
}

func (s *Module) registerEventSubscribers(cnt Container) {
	cnt.GetEventModule().Dispatcher.Subscribe(events.TypeDeploySuccess,
		service.NewSubscriber(s.ServiceStore,
			cnt.GetSwarm(),
			metadata.NewExtractor(),
		),
	)
}
