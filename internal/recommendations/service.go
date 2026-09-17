package recommendations

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

type Service struct {
	// Store persists and lists generated recommendations.
	Store modelstore.Store

	// Recommender analyzes stack state and updates stored recommendations.
	Recommender *Recommender

	// DeploySubscriber updates recommendations after deploy events.
	DeploySubscriber *RecommenderEventSubscriber
}

func InitService(ctx context.Context, path string, filesystem fs.FileSystem) (*Service, error) {
	store, err := modelstore.NewFileStore(ctx, path, filesystem)
	if err != nil {
		return nil, fmt.Errorf("init file store: %w", err)
	}

	recommender := NewRecommender(
		analyzer.Composite(
			analyzer.NewResourcesUnspecifiedAnalyzer(),
			analyzer.NewImageAnalyzer(),
		),
		store,
	)

	return &Service{
		Store:            store,
		Recommender:      recommender,
		DeploySubscriber: NewRecommenderEventSubscriber(recommender),
	}, nil
}

func (r *Service) RegisterEventSubscribers(eventDispatcher dispatcher.Dispatcher) {
	eventDispatcher.Subscribe(events.TypeDeploySuccess, r.DeploySubscriber)
	eventDispatcher.Subscribe(events.TypeDeployFailed, r.DeploySubscriber)
}
