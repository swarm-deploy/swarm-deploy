package alertmanagement

import (
	"context"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/evaluators"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Subscriber routes source events to evaluators and applies their actions serially.
type Subscriber struct {
	mu            sync.Mutex
	sync          evaluators.Evaluator
	gitRepository evaluators.Evaluator
	node          evaluators.Evaluator
	serviceDrift  evaluators.Evaluator
	configuration evaluators.Evaluator
	lifecycle     Lifecycle
}

func newSubscriber(store modelstore.Store, lifecycle Lifecycle, stacks config.StackProvider) *Subscriber {
	return &Subscriber{
		sync:          evaluators.NewSyncEvaluator(store, stacks),
		gitRepository: evaluators.NewGitRepositoryEvaluator(store),
		node:          evaluators.NewNodeEvaluator(store),
		serviceDrift:  evaluators.NewServiceDriftEvaluator(store),
		configuration: evaluators.NewConfigurationEvaluator(store, stacks),
		lifecycle:     lifecycle,
	}
}

// Name returns the subscriber name used in dispatcher logs.
func (s *Subscriber) Name() string { return "alert-management" }

// Handle evaluates and applies one supported source event.
func (s *Subscriber) Handle(ctx context.Context, event events.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	evaluator := s.evaluatorFor(event.Event)
	if evaluator == nil {
		return nil
	}
	actions, err := evaluator.Evaluate(ctx, event)
	if err != nil || len(actions) == 0 {
		return err
	}
	return s.lifecycle.Apply(ctx, actions)
}

func (s *Subscriber) evaluatorFor(event events.Event) evaluators.Evaluator {
	switch source := event.(type) {
	case *events.ConfigurationChanged:
		if source.PreviousRepository != "" {
			return s.gitRepository
		}
		return s.configuration
	case *events.DeployFailed, *events.DeployPreparationFailed, *events.DeployInterrupted, *events.DeploySuccess:
		return s.sync
	case *events.GitPullFailed, *events.GitPullSucceeded:
		return s.gitRepository
	case *events.NodeDisconnected, *events.NodeConnected:
		return s.node
	case *events.ServiceMissed, *events.ServiceDrifted, *events.ServiceDriftFixed, *events.ServiceRemovedFromDesired:
		return s.serviceDrift
	default:
		return nil
	}
}
