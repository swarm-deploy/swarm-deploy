package modelstore

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]model.Recommendation, error)

	UpdateStack(ctx context.Context, stack string, recommendations []model.Recommendation) error
}

type ListFilter struct {
	Stack string // Optional
}
