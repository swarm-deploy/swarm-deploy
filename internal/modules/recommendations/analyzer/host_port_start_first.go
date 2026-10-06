package analyzer

import (
	"context"
	"strings"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
)

// HostPortStartFirstAnalyzer detects host-published ports combined with start-first updates.
type HostPortStartFirstAnalyzer struct {
	now func() time.Time
}

// NewHostPortStartFirstAnalyzer creates a host-port/start-first recommendation analyzer.
func NewHostPortStartFirstAnalyzer() *HostPortStartFirstAnalyzer {
	return &HostPortStartFirstAnalyzer{now: time.Now}
}

// Analyze inspects services for host-published ports combined with start-first updates.
func (a *HostPortStartFirstAnalyzer) Analyze(_ context.Context, stack model.Stack) []model.Recommendation {
	recs := make([]model.Recommendation, 0)

	for _, service := range stack.Definition.Compose.Services {
		if !usesStartFirstUpdate(service) || !usesHostPublishedPort(service) {
			continue
		}

		recs = append(recs, model.Recommendation{
			Severity: model.SeverityMedium,
			Type:     model.TypeServiceHostPortStartFirst,
			Source:   model.SourceFromStack(stack),
			Subject: model.Subject{
				Stack:   stack.Name,
				Service: service.Name,
			},
			Title: "Host-published port is used with start-first updates",
			Recommendation: "Avoid combining ports with mode: host and deploy.update_config.order: start-first; " +
				"use stop-first or ingress publishing mode to prevent host-port conflicts during rolling updates",
			CreatedAt: a.now(),
		})
	}

	return recs
}

func usesStartFirstUpdate(service compose.Service) bool {
	if service.Deploy.UpdateConfig == nil {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(service.Deploy.UpdateConfig.Order), "start-first")
}

func usesHostPublishedPort(service compose.Service) bool {
	for _, port := range service.Ports.Ports {
		if port.Mode == dockerswarm.PortConfigPublishModeHost {
			return true
		}
	}

	return false
}
