package resources

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
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

	nodeStore := node.NewSQLStore(cnt.GetStorage())
	srv.NodeStore = nodeStore
	srv.ServiceStore = modelstore.NewSQLStore(cnt.GetStorage())

	srv.NodeCollector = node.NewNodeCollector(
		cnt.GetSwarm().Nodes, nodeStore, cnt.GetEventModule().Dispatcher, cnt.GetStorage(),
	)
	secretDomain, err := secrets.NewDomain(
		cnt.GetStorage(),
		cnt.GetSwarm().Secrets,
	)
	if err != nil {
		return nil, fmt.Errorf("init secrets domain: %w", err)
	}
	srv.Secrets = secretDomain
	srv.SecretManagers = secretmanager.NewDomain(srv.ServiceStore)

	if err = srv.registerEventSubscribers(cnt); err != nil {
		return nil, err
	}

	return srv, nil
}

func (s *Module) registerEventSubscribers(cnt Container) error {
	return cnt.GetEventModule().Dispatcher.Subscribe(events.TypeNameDeploySuccess, "service-metadata",
		outbox.External(deployment.WithDesired(deployment.NewStore(cnt.GetStorage()),
			service.NewSubscriber(s.ServiceStore, cnt.GetSwarm(), metadata.NewExtractor(),
				cnt.GetStorage(), cnt.GetEventModule().Dispatcher))))
}
