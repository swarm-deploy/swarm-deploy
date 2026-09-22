package recommendations

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
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

func (r *RecommenderEventSubscriber) Handle(ctx context.Context, event events.Envelope) error {
	stack, stackValid := r.stack(event)
	if !stackValid {
		return nil
	}

	return r.recommender.Recommend(ctx, model.Stack{
		Name:       stack.Name,
		Definition: stack.Definition,
		Commit:     stack.Commit,
	})
}

func (r *RecommenderEventSubscriber) stack(event events.Envelope) (model.Stack, bool) {
	var meta events.DeployEvent

	switch e := event.Payload.(type) {
	case *events.DeploySuccess:
		meta = e.DeployEvent
	case *events.DeployFailed:
		meta = e.DeployEvent
	default:
		return model.Stack{}, false
	}

	return model.Stack{
		Name:       meta.StackName,
		Definition: meta.StackDefinition,
		Commit:     meta.Commit,
	}, true
}
