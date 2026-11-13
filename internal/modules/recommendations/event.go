package recommendations

import (
	"context"
	"fmt"

	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

type RecommenderEventSubscriber struct {
	deployments *deploymentstore.Store
	recommender *Recommender
}

func NewRecommenderEventSubscriber(
	deployments *deploymentstore.Store,
	recommender *Recommender,
) *RecommenderEventSubscriber {
	return &RecommenderEventSubscriber{deployments: deployments, recommender: recommender}
}

func (r *RecommenderEventSubscriber) Name() string {
	return "Recommender"
}

func (r *RecommenderEventSubscriber) Handle(ctx context.Context, event events.Envelope) error {
	stack, stackValid, err := r.stack(ctx, event)
	if err != nil {
		return err
	}
	if !stackValid {
		return nil
	}

	return r.recommender.Recommend(ctx, model.Stack{
		Name:       stack.Name,
		Definition: stack.Definition,
		Commit:     stack.Commit,
	})
}

func (r *RecommenderEventSubscriber) stack(ctx context.Context, event events.Envelope) (model.Stack, bool, error) {
	var meta events.DeployEvent

	switch e := event.Event.(type) {
	case *events.DeploySuccess:
		meta = e.DeployEvent
	case *events.DeployFailed:
		meta = e.DeployEvent
	default:
		return model.Stack{}, false, nil
	}
	if meta.DeploymentID == "" {
		return model.Stack{}, false, fmt.Errorf("deployment reference missing")
	}
	desired, err := r.deployments.Desired(ctx, meta.DeploymentID)
	if err != nil {
		return model.Stack{}, false, err
	}

	return model.Stack{
		Name:       meta.StackName,
		Definition: desired,
		Commit:     meta.Commit,
	}, true, nil
}
