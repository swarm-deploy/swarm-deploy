package modelstore

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

type Store interface {
	// List returns recommendations matching filter.
	List(ctx context.Context, filter ListFilter) ([]model.Recommendation, error)

	// UpdateStack replaces recommendations for stack.
	UpdateStack(ctx context.Context, stack string, recommendations []model.Recommendation) error
}

type ListFilter struct {
	// Stack filters recommendations by stack name.
	Stack string
	// Limit limits the number of returned recommendations.
	Limit int
}
