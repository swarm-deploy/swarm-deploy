package modelstore

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
)

type Store interface {
	UpdateStack(ctx context.Context, stack string, recommendations []model.Recommendation) error
}
