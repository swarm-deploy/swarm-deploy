package evaluators

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// ConfigurationEvaluator evaluates incidents and counters for removed stacks.
type ConfigurationEvaluator struct {
	store  modelstore.Store
	stacks config.StackProvider
}

// NewConfigurationEvaluator reads current incidents and desired stacks.
func NewConfigurationEvaluator(store modelstore.Store, stacks config.StackProvider) *ConfigurationEvaluator {
	return &ConfigurationEvaluator{store: store, stacks: stacks}
}

// Evaluate returns a counter reset and the resolutions for removed stack resources.
func (e *ConfigurationEvaluator) Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error) {
	configuration, ok := event.Event.(*events.ConfigurationChanged)
	if !ok || configuration.PreviousRepository != "" {
		return nil, nil
	}
	// Retry retained events until the initial Git reload supplies the desired configuration.
	if readiness, hasReadiness := e.stacks.(interface{ Ready() bool }); hasReadiness && !readiness.Ready() {
		return nil, errors.New("desired configuration has not completed its initial reload")
	}

	desired := make(map[string]struct{})
	desiredFingerprints := make([]string, 0)
	for _, stack := range e.stacks.Stacks() {
		desired[stack.Name] = struct{}{}
		desiredFingerprints = append(desiredFingerprints, model.SyncFailedFingerprint(stack.Name))
	}
	alerts, err := e.store.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	if err != nil {
		return nil, fmt.Errorf("list open alerts for configuration reconciliation: %w", err)
	}
	actions := []model.AlertAction{model.NewResetFailureStreaksExceptAction(desiredFingerprints)}
	for _, alert := range alerts {
		stackName, relevant := alertStackName(alert)
		if !relevant {
			continue
		}
		if _, exists := desired[stackName]; exists {
			continue
		}
		message := "Stack removed from desired configuration"
		if alert.Kind == model.AlertKindServiceDrift {
			message = "Service removed with stack from desired configuration"
		}
		actions = append(actions, model.NewResolveAction(
			alert.Fingerprint, event.ID, model.ResolutionReasonResourceRemoved, message,
		))
	}
	return actions, nil
}

func alertStackName(alert model.Alert) (string, bool) {
	switch {
	case alert.ResourceType == model.ResourceTypeStack:
		return alert.ResourceID, true
	case alert.Kind == model.AlertKindServiceDrift && alert.ResourceType == model.ResourceTypeService:
		stackName, _, ok := strings.Cut(alert.ResourceID, "/")
		return stackName, ok
	default:
		return "", false
	}
}
