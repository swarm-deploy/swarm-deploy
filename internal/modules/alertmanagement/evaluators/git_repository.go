package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// GitRepositoryEvaluator evaluates repository access and removal events.
type GitRepositoryEvaluator struct {
	store modelstore.Store
}

// NewGitRepositoryEvaluator reads open Git repository incidents.
func NewGitRepositoryEvaluator(store modelstore.Store) *GitRepositoryEvaluator {
	return &GitRepositoryEvaluator{store: store}
}

// Evaluate returns repository failure, recovery or removal actions.
func (e *GitRepositoryEvaluator) Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.GitPullFailed:
		openAfter := openImmediately
		if source.Category == events.GitFailureNetwork {
			openAfter = temporaryFailureThreshold
		}
		alert, err := alertForFailure(ctx, e.store, model.Alert{
			Fingerprint: model.GitRepositoryUnavailableFingerprint(source.Repository),
			Kind:        model.AlertKindGitRepositoryUnavailable, ResourceType: model.ResourceTypeRepository,
			ResourceID: source.Repository, Title: "Git repository unavailable", FailureStage: "git_pull",
		})
		if err != nil {
			return nil, err
		}
		alert.Message, alert.ErrorCode, alert.LatestEventID = source.Description, string(source.Category), event.ID
		return []model.AlertAction{model.NewRecordFailureAction(alert, openAfter, true)}, nil
	case *events.GitPullSucceeded:
		fingerprint := model.GitRepositoryUnavailableFingerprint(source.Repository)
		return []model.AlertAction{
			model.NewResetFailureStreakAction(fingerprint),
			model.NewResolveAction(fingerprint, event.ID, model.ResolutionReasonRecovered, "Git repository access recovered"),
		}, nil
	case *events.ConfigurationChanged:
		if source.PreviousRepository == "" {
			return nil, nil
		}
		fingerprint := model.GitRepositoryUnavailableFingerprint(source.PreviousRepository)
		return []model.AlertAction{
			model.NewResetFailureStreakAction(fingerprint),
			model.NewResolveAction(fingerprint, event.ID,
				model.ResolutionReasonResourceRemoved, "Git repository removed from configuration"),
		}, nil
	default:
		return nil, nil
	}
}
