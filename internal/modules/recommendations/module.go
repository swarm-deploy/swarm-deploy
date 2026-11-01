package recommendations

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/analyzer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
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
	// GetStorage returns the shared database.
	GetStorage() storage.Database
	GetEventModule() *event.Module
}

func InitModule(ctx context.Context, cfg *config.Config, cnt Container) (*Module, error) {
	store := modelstore.NewSQLStore(cnt.GetStorage())

	recommender := NewRecommender(
		analyzer.Composite(
			analyzer.NewResourcesUnspecifiedAnalyzer(),
			analyzer.NewImageAnalyzer(),
			analyzer.NewRestartPolicyUnspecifiedAnalyzer(),
			analyzer.NewScheduledServiceSelfHealAnalyzer(cfg),
			analyzer.NewHostPortStartFirstAnalyzer(),
			analyzer.NewSwarmDeployDockerConfigAnalyzer(),
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

	subscriber := &orderedSubscriber{
		db: cnt.GetStorage(), next: deployment.WithDesired(deploymentstore.NewStore(cnt.GetStorage()), m.DeploySubscriber),
	}
	if err := m.registerEventSubscribers(cnt.GetEventModule().Dispatcher, subscriber); err != nil {
		return nil, err
	}

	return m, nil
}

func (r *Module) registerEventSubscribers(
	eventDispatcher dispatcher.Dispatcher,
	subscriber dispatcher.Subscriber,
) error {
	return dispatcher.SubscribeMany(eventDispatcher,
		[]events.TypeName{events.TypeNameDeploySuccess, events.TypeNameDeployFailed}, "recommendations", subscriber)
}
