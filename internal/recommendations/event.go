package recommendations

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type RecommenderEventSubscriber struct {
	recommender *Recommender
}

func NewRecommenderEventSubscriber(recommender *Recommender) *RecommenderEventSubscriber {
	return &RecommenderEventSubscriber{recommender: recommender}
}

func (r *RecommenderEventSubscriber) Name() string {
	return "Recommender"
}

func (r *RecommenderEventSubscriber) Slow() bool {
	return false
}

func (r *RecommenderEventSubscriber) Handle(ctx context.Context, event events.Event) error {
	deployEvent, isDeployEvent := event.(*events.DeploySuccess)
	if !isDeployEvent {
		return nil
	}

	return r.recommender.Recommend(ctx, deployEvent.StackName, deployEvent.StackDefinition)
}
