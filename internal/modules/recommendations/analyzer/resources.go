package analyzer

import (
	"context"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

type ResourcesUnspecifiedAnalyzer struct {
	now func() time.Time
}

func NewResourcesUnspecifiedAnalyzer() *ResourcesUnspecifiedAnalyzer {
	return &ResourcesUnspecifiedAnalyzer{
		now: time.Now,
	}
}

func (a *ResourcesUnspecifiedAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		rec := a.analyze(service)
		if rec == nil {
			continue
		}

		rec.Subject = model.Subject{
			Stack:   stack.Name,
			Service: service.Name,
		}
		rec.CreatedAt = a.now()
		rec.Source = model.SourceFromStack(stack)
		recs = append(recs, *rec)
	}

	return recs
}

func (a *ResourcesUnspecifiedAnalyzer) analyze(srv compose.Service) *model.Recommendation {
	if srv.Deploy.Resources == nil {
		return &model.Recommendation{
			Severity:       model.SeverityMedium,
			Type:           model.TypeServiceResourcesUnspecified,
			Title:          "Service with unspecified resources",
			Recommendation: "Specify deploy.resources for the service",
		}
	}

	if srv.Deploy.Resources.Limits == nil {
		return &model.Recommendation{
			Severity:       model.SeverityMedium,
			Type:           model.TypeServiceResourcesLimitsUnspecified,
			Title:          "Service with unspecified limits of resources",
			Recommendation: "Specify deploy.resources.limits for the service",
		}
	}

	return nil
}
