package resources

import (
	"context"
	"fmt"
	"path/filepath"

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
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Module struct {
	NodeStore     *node.Store
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

	if err := srv.initStores(ctx, cnt.GetFileSystem()); err != nil {
		return nil, fmt.Errorf("init stores: %w", err)
	}

	srv.NodeCollector = node.NewNodeCollector(cnt.GetSwarm().Nodes, srv.NodeStore, cnt.GetEventModule().Dispatcher)
	secretDomain, err := secrets.NewDomain(
		ctx,
		cfg.Spec.DataDir,
		cnt.GetFileSystem(),
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
			cnt.GetFileSystem(),
			metadata.NewExtractor(),
		),
	)
}

func (s *Module) initStores(ctx context.Context, filesystem fs.FileSystem) error {
	nodeStore, err := node.NewNodeStore(filepath.Join(s.cfg.Spec.DataDir, "nodes.json"))
	if err != nil {
		return fmt.Errorf("init node store: %w", err)
	}

	s.NodeStore = nodeStore

	srvStore, err := modelstore.NewFileStore(ctx, filepath.Join(s.cfg.Spec.DataDir, "services.json"), filesystem)
	if err != nil {
		return fmt.Errorf("init service store: %w", err)
	}

	s.ServiceStore = srvStore

	return nil
}
