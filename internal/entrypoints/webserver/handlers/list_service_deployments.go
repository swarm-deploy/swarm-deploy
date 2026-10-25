package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
)

const serviceDeploymentsLimit = 5

func (h *handler) ListServiceDeployments(
	ctx context.Context,
	params generated.ListServiceDeploymentsParams,
) (*generated.ServiceDeploymentsResponse, error) {
	records, err := h.deployments.ListServiceDeployments(
		ctx, params.Stack, params.Service, int(params.Limit.Or(serviceDeploymentsLimit)),
	)
	if err != nil {
		return nil, err
	}
	items := make([]generated.ServiceDeploymentResponse, 0, len(records))
	for _, record := range records {
		item := generated.ServiceDeploymentResponse{
			ID:           record.ID,
			CreatedAt:    record.StartedAt,
			Status:       generated.ServiceDeploymentStatus(record.Status),
			Image:        record.Image,
			ImageVersion: utils.ImageVersion(record.Image),
			Commit:       toOptString(record.Commit),
		}
		items = append(items, item)
	}
	return &generated.ServiceDeploymentsResponse{Deployments: items}, nil
}
