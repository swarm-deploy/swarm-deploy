package analyzer

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

const swarmCronjobEnableLabel = "swarm.cronjob.enable"

// RestartPolicyUnspecifiedAnalyzer recommends explicit restart policies for long-running services.
type RestartPolicyUnspecifiedAnalyzer struct {
	now func() time.Time
}

// NewRestartPolicyUnspecifiedAnalyzer creates a restart-policy recommendation analyzer.
func NewRestartPolicyUnspecifiedAnalyzer() *RestartPolicyUnspecifiedAnalyzer {
	return &RestartPolicyUnspecifiedAnalyzer{now: time.Now}
}

// Analyze inspects restart policies of long-running Compose services.
func (a *RestartPolicyUnspecifiedAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		if service.Deploy.RestartPolicy != nil || isJobService(service) {
			continue
		}

		recs = append(recs, model.Recommendation{
			Severity: model.SeverityLow,
			Type:     model.TypeServiceRestartPolicyUnspecified,
			Source:   model.SourceFromStack(stack),
			Subject: model.Subject{
				Stack:   stack.Name,
				Service: service.Name,
			},
			Title:          "Restart policy is unspecified",
			Recommendation: "Explicitly define deploy.restart_policy so restart behavior is documented and predictable",
			CreatedAt:      a.now(),
		})
	}

	return recs
}

func isJobService(service compose.Service) bool {
	mode := strings.ToLower(strings.TrimSpace(service.Deploy.Mode))
	if mode == "replicated-job" || mode == "global-job" {
		return true
	}

	enabled, err := strconv.ParseBool(strings.TrimSpace(service.Deploy.Labels.Map[swarmCronjobEnableLabel]))
	return err == nil && enabled
}
