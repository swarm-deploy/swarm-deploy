package handlers

import (
	"context"
	"errors"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
)

func (h *handler) ListDeployments(
	ctx context.Context, params generated.ListDeploymentsParams,
) (*generated.DeploymentsResponse, error) {
	items, err := h.deployments.List(ctx, deployment.ListFilter{
		Stack: params.Stack.Or(""), Limit: int(params.Limit.Or(0)),
	})
	if err != nil {
		return nil, err
	}
	result := &generated.DeploymentsResponse{Deployments: make([]generated.Deployment, 0, len(items))}
	for _, item := range items {
		result.Deployments = append(result.Deployments, toGeneratedDeployment(item))
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

func toGeneratedDeployment(item deployment.Deployment) generated.Deployment {
	result := generated.Deployment{ID: item.ID, Stack: item.Stack, Commit: item.Commit,
		Status:    generated.DeploymentStatus(item.Status),
		StartedAt: item.StartedAt, Changes: make([]generated.DeploymentChange, 0, len(item.Changes))}
	if item.FinishedAt != nil {
		result.FinishedAt = generated.NewOptDateTime(*item.FinishedAt)
	}
	if item.ErrorCode != "" {
		result.ErrorCode = generated.NewOptString(item.ErrorCode)
	}
	for _, change := range item.Changes {
		mapped := generated.DeploymentChange{Path: change.Path, Redacted: change.Redacted}
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
