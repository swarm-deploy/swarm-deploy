package rag

import (
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/webroute"
)

// ServiceDocumentBuilder builds RAG document text from service metadata.
type ServiceDocumentBuilder struct{}

// NewServiceDocumentBuilder creates default service document builder.
func NewServiceDocumentBuilder() *ServiceDocumentBuilder {
	return &ServiceDocumentBuilder{}
}

// Build transforms service metadata into a searchable text document.
func (*ServiceDocumentBuilder) Build(serviceInfo service.Info) string {
	document := fmt.Sprintf(
		"stack=%s service=%s type=%s image=%s description=%s",
		serviceInfo.Stack,
		serviceInfo.Name,
		serviceInfo.Type,
		serviceInfo.Image,
		serviceInfo.Description,
	)
	webRoutesPart := webRoutesToDocumentPart(serviceInfo.WebRoutes)
	if webRoutesPart != "" {
		document += " " + webRoutesPart
	}

	return strings.TrimSpace(document)
}

func webRoutesToDocumentPart(routes []webroute.Route) string {
	if len(routes) == 0 {
		return ""
	}

	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		parts = append(
			parts,
			fmt.Sprintf("web_route_domain=%s web_route_address=%s web_route_port=%s", route.Domain, route.Address, route.Port),
		)
	}

	return strings.Join(parts, " ")
}
