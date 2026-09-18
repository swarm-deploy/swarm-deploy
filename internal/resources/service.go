package resources

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/node"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type Service struct {
	NodeStore     *node.Store
	NodeCollector *node.Collector
	ServiceStore  *service.Store

	cfg      *config.Config
	swarmSvc *swarm.Swarm
}

func InitService(
	ctx context.Context,
	cfg *config.Config,
	swarmSvc *swarm.Swarm,
	eventDispatcher dispatcher.Dispatcher,
	filesystem fs.FileSystem,
) (*Service, error) {
	srv := &Service{
		cfg: cfg,
	}

	if err := srv.initStores(ctx, filesystem); err != nil {
		return nil, fmt.Errorf("init stores: %w", err)
	}

	srv.NodeCollector = node.NewNodeCollector(swarmSvc.Nodes, srv.NodeStore, eventDispatcher)

	return srv, nil
}

func (s *Service) RegisterEventSubscribers(eventDispatcher dispatcher.Dispatcher) {
	eventDispatcher.Subscribe(events.TypeDeploySuccess,
		service.NewSubscriber(s.ServiceStore,
			s.swarmSvc.Services,
			s.swarmSvc.Images,
			s.swarmSvc.Configs,
			metadata.NewExtractor(),
		),
	)
}

func (s *Service) initStores(ctx context.Context, filesystem fs.FileSystem) error {
	nodeStore, err := node.NewNodeStore(filepath.Join(s.cfg.Spec.DataDir, "nodes.json"))
	if err != nil {
		return fmt.Errorf("init node store: %w", err)
	}

	s.NodeStore = nodeStore

	srvStore, err := service.NewStore(ctx, filepath.Join(s.cfg.Spec.DataDir, "services.json"), filesystem)
	if err != nil {
		return fmt.Errorf("init service store: %w", err)
	}

	s.ServiceStore = srvStore

	return nil
}
