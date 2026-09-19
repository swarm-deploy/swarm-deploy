package recommendations

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/event"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

type Module struct {
	// Store persists and lists generated recommendations.
	Store modelstore.Store

	// Recommender analyzes stack state and updates stored recommendations.
	Recommender *Recommender

	// DeploySubscriber updates recommendations after deploy events.
	DeploySubscriber *RecommenderEventSubscriber
}

type Container interface {
	GetFileSystem() fs.FileSystem
	GetEventModule() *event.Module
}

func InitModule(ctx context.Context, cfg *config.Config, cnt Container) (*Module, error) {
	store, err := modelstore.NewFileStore(ctx,
		filepath.Join(cfg.Spec.DataDir, "recommendations.state.json"),
		cnt.GetFileSystem(),
	)
	if err != nil {
		return nil, fmt.Errorf("init file store: %w", err)
	}

	recommender := NewRecommender(
		analyzer.Composite(
			analyzer.NewResourcesUnspecifiedAnalyzer(),
			analyzer.NewImageAnalyzer(),
			analyzer.NewRestartPolicyUnspecifiedAnalyzer(),
			analyzer.NewDockerSocketMountAnalyzer(),
			analyzer.NewServiceCapabilitiesAnalyzer(),
		),
		store,
	)

	m := &Module{
		Store:            store,
		Recommender:      recommender,
		DeploySubscriber: NewRecommenderEventSubscriber(recommender),
	}

	m.registerEventSubscribers(cnt.GetEventModule().Dispatcher)

	return m, nil
}

func (r *Module) registerEventSubscribers(eventDispatcher dispatcher.Dispatcher) {
	eventDispatcher.Subscribe(events.TypeDeploySuccess, r.DeploySubscriber)
	eventDispatcher.Subscribe(events.TypeDeployFailed, r.DeploySubscriber)
}
