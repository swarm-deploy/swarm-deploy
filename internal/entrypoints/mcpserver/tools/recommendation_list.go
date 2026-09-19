package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/modelstore"
)

const maxRecommendationsLimit = 200

// RecommendationList returns stored recommendations.
type RecommendationList struct {
	recommendations RecommendationsReader
}

type recommendationListRequest struct {
	Stack string `json:"stack"`
	Limit *int   `json:"limit"`
}

// NewRecommendationList creates recommendation_list component.
func NewRecommendationList(recommendations RecommendationsReader) *RecommendationList {
	return &RecommendationList{recommendations: recommendations}
}

// Definition returns tool metadata visible to the model.
func (l *RecommendationList) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "recommendation_list",
		Description: "Returns stored deployment recommendations, optionally filtered by stack.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"stack": map[string]any{
					"type":        "string",
					"description": "Optional stack name to filter recommendations.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of recommendations to return.",
					"minimum":     1,
					"maximum":     maxRecommendationsLimit,
				},
			},
		},
		Request: recommendationListRequest{},
	}
}

// Execute runs recommendation_list tool.
func (l *RecommendationList) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[recommendationListRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	filter, err := parseRecommendationListFilter(parsedRequest)
	if err != nil {
		return routing.Response{}, err
	}

	recommendations, err := l.recommendations.List(ctx, filter)
	if err != nil {
		return routing.Response{}, fmt.Errorf("list recommendations: %w", err)
	}

	payload := struct {
		// Recommendations contains stored deployment recommendations.
		Recommendations []model.Recommendation `json:"recommendations"`
	}{
		Recommendations: recommendations,
	}

	return routing.Response{Payload: payload}, nil
}

func parseRecommendationListFilter(request recommendationListRequest) (modelstore.ListFilter, error) {
	filter := modelstore.ListFilter{
		Stack: strings.TrimSpace(request.Stack),
	}

	if request.Limit == nil {
		return filter, nil
	}

	limit := *request.Limit
	if limit <= 0 {
		return modelstore.ListFilter{}, fmt.Errorf("limit must be > 0")
	}
	if limit > maxRecommendationsLimit {
		return modelstore.ListFilter{}, fmt.Errorf("limit must be <= %d", maxRecommendationsLimit)
	}

	filter.Limit = limit
	return filter, nil
}
