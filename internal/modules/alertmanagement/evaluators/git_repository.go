package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// GitRepositoryEvaluator evaluates repository access and removal events.
type GitRepositoryEvaluator struct{}

// Evaluate returns repository failure, recovery or removal actions.
func (GitRepositoryEvaluator) Evaluate(_ context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.GitPullFailed:
		openAfter := openImmediately
		if source.Category == events.GitFailureNetwork {
			openAfter = temporaryFailureThreshold
		}
		return []model.AlertAction{{
			Kind: model.RecordFailure, OpenAfter: openAfter, TrackFailureStreak: true,
			Alert: model.Alert{
				Fingerprint: model.GitRepositoryUnavailableFingerprint(source.Repository),
				Kind:        model.AlertKindGitRepositoryUnavailable, ResourceType: model.ResourceTypeRepository,
				ResourceID: source.Repository, Title: "Git repository unavailable", Message: source.Description,
				FailureStage: "git_pull", ErrorCode: string(source.Category), LatestEventID: event.ID,
			},
		}}, nil
	case *events.GitPullSucceeded:
		fingerprint := model.GitRepositoryUnavailableFingerprint(source.Repository)
		return []model.AlertAction{
			resetFailureStreak(fingerprint),
			resolve(fingerprint, event.ID, model.ResolutionReasonRecovered, "Git repository access recovered"),
		}, nil
	case *events.ConfigurationChanged:
		if source.PreviousRepository == "" {
			return nil, nil
		}
		fingerprint := model.GitRepositoryUnavailableFingerprint(source.PreviousRepository)
		return []model.AlertAction{
			resetFailureStreak(fingerprint),
			resolve(fingerprint, event.ID, model.ResolutionReasonResourceRemoved, "Git repository removed from configuration"),
		}, nil
	default:
		return nil, nil
	}
}
