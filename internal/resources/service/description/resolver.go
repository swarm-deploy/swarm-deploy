package description

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

// Labels groups metadata labels from different inspection scopes.
type Labels struct {
	// Service contains labels from docker service annotations.
	Service map[string]string
	// Container contains labels from service task container spec.
	Container map[string]string
	// Image contains labels from OCI image config.
	Image map[string]string
}

// Resolver resolves service description using labels priority.
type Resolver struct{}

// NewResolver creates description resolver.
func NewResolver() *Resolver {
	return &Resolver{}
}

// Resolve resolves service description from labels by priority.
func (*Resolver) Resolve(labels Labels) string {
	return firstNonEmptyLabel(
		[]map[string]string{
			labels.Service,
			labels.Container,
			labels.Service,
			labels.Container,
			labels.Image,
			labels.Service,
			labels.Container,
			labels.Image,
		},
		[]string{
			labelsdict.ServiceDescription,
			labelsdict.ServiceDescription,
			labelsdict.OCIImageDescription,
			labelsdict.OCIImageDescription,
			labelsdict.OCIImageDescription,
			labelsdict.OCIImageTitle,
			labelsdict.OCIImageTitle,
			labelsdict.OCIImageTitle,
		},
	)
}

func firstNonEmptyLabel(sources []map[string]string, keys []string) string {
	for i, source := range sources {
		if source == nil {
			continue
		}

		value := strings.TrimSpace(source[keys[i]])
		if value != "" {
			return value
		}
	}

	return ""
}
