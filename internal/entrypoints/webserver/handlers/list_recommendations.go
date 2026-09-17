package handlers

import (
	"context"
	"fmt"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
)

func (h *handler) ListRecommendations(
	ctx context.Context,
	params generated.ListRecommendationsParams,
) (*generated.RecommendationsResponse, error) {
	recommendations, err := h.recommendations.List(ctx, modelstore.ListFilter{
		Stack: params.Stack.Or(""),
		Limit: int(params.Limit.Or(0)),
	})
	if err != nil {
		return nil, fmt.Errorf("list recommendations: %w", err)
	}

	return &generated.RecommendationsResponse{
		Recommendations: toGeneratedRecommendations(recommendations),
	}, nil
}

func toGeneratedRecommendations(recommendations []model.Recommendation) []generated.Recommendation {
	items := make([]generated.Recommendation, 0, len(recommendations))
	for _, recommendation := range recommendations {
		items = append(items, generated.Recommendation{
			Severity: string(recommendation.Severity),
			Type:     string(recommendation.Type),
			Subject: generated.RecommendationSubject{
				Stack:   recommendation.Subject.Stack,
				Service: recommendation.Subject.Service,
			},
			Recommendation: recommendation.Recommendation,
		})
	}

	return items
}
