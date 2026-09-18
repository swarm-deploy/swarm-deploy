package analyzer

import (
	"context"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/recommendations/model"
)

var highRiskCapabilities = map[string]struct{}{
	"SYS_ADMIN":  {},
	"SYS_MODULE": {},
}

var mediumRiskCapabilities = map[string]struct{}{
	"NET_ADMIN":  {},
	"NET_RAW":    {},
	"SYS_PTRACE": {},
}

// ServiceCapabilitiesAnalyzer detects broadly privileged and sensitive Linux capabilities.
type ServiceCapabilitiesAnalyzer struct {
	now func() time.Time
}

// NewServiceCapabilitiesAnalyzer creates a service capability recommendation analyzer.
func NewServiceCapabilitiesAnalyzer() *ServiceCapabilitiesAnalyzer {
	return &ServiceCapabilitiesAnalyzer{now: time.Now}
}

// Analyze inspects capabilities added to Compose services.
func (a *ServiceCapabilitiesAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		for _, recommendation := range a.analyze(service) {
			recommendation.Source = model.SourceFromStack(stack)
			recommendation.Subject = model.Subject{Stack: stack.Name, Service: service.Name}
			recommendation.CreatedAt = a.now()
			recs = append(recs, recommendation)
		}
	}

	return recs
}

func (a *ServiceCapabilitiesAnalyzer) analyze(service compose.Service) []model.Recommendation {
	hasAll := false
	hasHighRisk := false
	hasMediumRisk := false

	for _, capability := range service.CapAdd {
		normalized := strings.ToUpper(strings.TrimSpace(capability))
		if normalized == "ALL" {
			hasAll = true
		}
		if _, ok := highRiskCapabilities[normalized]; ok {
			hasHighRisk = true
		}
		if _, ok := mediumRiskCapabilities[normalized]; ok {
			hasMediumRisk = true
		}
	}

	recs := make([]model.Recommendation, 0)
	if hasAll {
		recs = append(recs, model.Recommendation{
			Severity:       model.SeverityHigh,
			Type:           model.TypeServiceCapabilitiesAll,
			Title:          "All Linux capabilities are added",
			Recommendation: "Avoid cap_add: ALL because it grants the service every Linux capability",
		})
	}
	if hasHighRisk {
		recs = append(recs, model.Recommendation{
			Severity: model.SeverityHigh,
			Type:     model.TypeServiceCapabilitiesPrivileged,
			Title:    "Highly privileged Linux capabilities are added",
			Recommendation: "Avoid adding SYS_ADMIN or SYS_MODULE unless strictly required because they grant " +
				"broad control over the host kernel and system",
		})
	}
	if hasMediumRisk {
		recs = append(recs, model.Recommendation{
			Severity: model.SeverityMedium,
			Type:     model.TypeServiceCapabilitiesSensitive,
			Title:    "Sensitive Linux capabilities are added",
			Recommendation: "Avoid adding NET_ADMIN, SYS_PTRACE, or NET_RAW unless the service explicitly " +
				"requires them",
		})
	}

	return recs
}
