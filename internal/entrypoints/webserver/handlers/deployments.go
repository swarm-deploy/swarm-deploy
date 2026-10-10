package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
)

func (h *handler) ListDeployments(
	ctx context.Context, params generated.ListDeploymentsParams,
) (*generated.DeploymentsResponse, error) {
	page, err := h.deployments.List(ctx, deployment.ListFilter{
		Stack: params.Stack.Or(""), Limit: int(params.Limit.Or(0)), Cursor: params.Cursor.Or(""),
	})
	if errors.Is(err, deployment.ErrInvalidCursor) {
		return nil, withStatusError(http.StatusBadRequest, err)
	}
	if err != nil {
		return nil, err
	}
	result := &generated.DeploymentsResponse{
		Deployments: make([]generated.DeploymentSummary, 0, len(page.Deployments)),
	}
	for _, item := range page.Deployments {
		result.Deployments = append(result.Deployments, toGeneratedDeploymentSummary(item))
	}
	if page.NextCursor != "" {
		result.NextCursor = generated.NewOptString(page.NextCursor)
	}
	return result, nil
}

func (h *handler) GetDeployment(
	ctx context.Context, params generated.GetDeploymentParams,
) (*generated.Deployment, error) {
	item, err := h.deployments.Get(ctx, params.ID)
	if errors.Is(err, deployment.ErrNotFound) {
		return nil, withStatusError(http.StatusNotFound, err)
	}
	if err != nil {
		return nil, err
	}
	result := toGeneratedDeployment(item)
	return &result, nil
}

func toGeneratedDeploymentSummary(item deployment.DeploymentSummary) generated.DeploymentSummary {
	result := generated.DeploymentSummary{
		ID: item.ID, Stack: item.Stack, Commit: item.Commit,
		Status:             generated.DeploymentStatus(item.Status),
		Phase:              generated.DeploymentPhase(item.Phase),
		ApplyStatus:        generated.DeploymentStageStatus(item.ApplyStatus),
		VerificationStatus: generated.DeploymentStageStatus(item.VerificationStatus),
		CleanupStatus:      generated.DeploymentStageStatus(item.CleanupStatus),
		ActualStateStatus:  generated.DeploymentActualStateStatus(item.ActualStateStatus),
		ComparisonBasis:    generated.DeploymentComparisonBasis(item.ComparisonBasis),
		ComparisonStatus:   generated.DeploymentComparisonStatus(item.ComparisonStatus),
		StartedAt:          item.StartedAt,
		Summary: generated.DeploymentChangeSummary{
			Added: item.Summary.Added, Changed: item.Summary.Changed,
			Removed: item.Summary.Removed, Redacted: item.Summary.Redacted,
		},
		Resources: generated.DeploymentResourceSummary{
			Services: item.Resources.Services, Configs: item.Resources.Configs, Secrets: item.Resources.Secrets,
		},
	}
	setGeneratedDeploymentSummaryOptional(&result, item.ErrorCode, item.BasisDeploymentID,
		item.StartedAt, item.FinishedAt, item.ObservedAt)
	return result
}

func toGeneratedDeployment(item deployment.Deployment) generated.Deployment {
	result := generated.Deployment{
		ID: item.ID, Stack: item.Stack, Commit: item.Commit,
		Status:             generated.DeploymentStatus(item.Status),
		Phase:              generated.DeploymentPhase(item.Phase),
		ApplyStatus:        generated.DeploymentStageStatus(item.ApplyStatus),
		VerificationStatus: generated.DeploymentStageStatus(item.VerificationStatus),
		CleanupStatus:      generated.DeploymentStageStatus(item.CleanupStatus),
		ActualStateStatus:  generated.DeploymentActualStateStatus(item.ActualStateStatus),
		ComparisonBasis:    generated.DeploymentComparisonBasis(item.ComparisonBasis),
		ComparisonStatus:   generated.DeploymentComparisonStatus(item.ComparisonStatus),
		StartedAt:          item.StartedAt,
		Summary: generated.DeploymentChangeSummary{
			Added: item.Summary.Added, Changed: item.Summary.Changed,
			Removed: item.Summary.Removed, Redacted: item.Summary.Redacted,
		},
		Resources: generated.DeploymentResourceSummary{
			Services: item.Resources.Services, Configs: item.Resources.Configs, Secrets: item.Resources.Secrets,
		},
		Changes: make([]generated.DeploymentChange, 0, len(item.Changes)),
	}
	if item.ErrorCode != "" {
		result.Reason = generated.NewOptString(item.ErrorCode)
	}
	if item.BasisDeploymentID != "" {
		result.BasisDeploymentID = generated.NewOptString(item.BasisDeploymentID)
	}
	setGeneratedTimes(item.StartedAt, item.FinishedAt, item.ObservedAt,
		&result.FinishedAt, &result.ObservedAt, &result.DurationMs)
	for _, change := range item.Changes {
		mapped := generated.DeploymentChange{
			ResourceType: change.ResourceType, ResourceName: change.ResourceName, Field: change.Field,
			Operation: generated.DeploymentChangeOperation(change.Operation), Redacted: change.Redacted,
		}
		if change.Before != nil {
			mapped.Before = generated.NewOptString(*change.Before)
		}
		if change.After != nil {
			mapped.After = generated.NewOptString(*change.After)
		}
		result.Changes = append(result.Changes, mapped)
	}
	return result
}

func setGeneratedDeploymentSummaryOptional(
	result *generated.DeploymentSummary,
	reason string,
	basisID string,
	started time.Time,
	finished *time.Time,
	observed *time.Time,
) {
	if reason != "" {
		result.Reason = generated.NewOptString(reason)
	}
	if basisID != "" {
		result.BasisDeploymentID = generated.NewOptString(basisID)
	}
	setGeneratedTimes(started, finished, observed, &result.FinishedAt, &result.ObservedAt, &result.DurationMs)
}

func setGeneratedTimes(
	started time.Time,
	finished *time.Time,
	observed *time.Time,
	finishedResult *generated.OptDateTime,
	observedResult *generated.OptDateTime,
	durationResult *generated.OptInt64,
) {
	if finished != nil {
		*finishedResult = generated.NewOptDateTime(*finished)
		duration := finished.Sub(started).Milliseconds()
		if duration < 0 {
			duration = 0
		}
		*durationResult = generated.NewOptInt64(duration)
	}
	if observed != nil {
		*observedResult = generated.NewOptDateTime(*observed)
	}
}
