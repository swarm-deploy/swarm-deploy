package analyzer

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
)

type ResourcesUnspecifiedAnalyzer struct {
}

func (a *ResourcesUnspecifiedAnalyzer) Analyze(_ context.Context, stack compose.File) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Compose.Services {
		rec := a.analyze(service)
		if rec == nil {
			continue
		}

		recs = append(recs, *rec)
	}

	return recs
}

func (a *ResourcesUnspecifiedAnalyzer) analyze(srv compose.Service) *model.Recommendation {
	if srv.Deploy.Resources == nil {
		return &model.Recommendation{
			Severity: model.SeverityMedium,
			Type:     model.TypeServiceResourcesUnspecified,
		}
	}

	if srv.Deploy.Resources.Limits == nil {
		return &model.Recommendation{
			Severity: model.SeverityMedium,
			Type:     model.TypeServiceResourcesLimitsUnspecified,
		}
	}

	return nil
}
