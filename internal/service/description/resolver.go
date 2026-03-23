package description

import "strings"

const (
	// LabelService is a service/container label with human-readable description.
	LabelService = "org.swarm-deploy.service.description"
	// LabelImageTitle is OCI image label with short title.
	LabelImageTitle = "org.opencontainers.image.title"
	// LabelImageDescription is OCI image label with extended description.
	LabelImageDescription = "org.opencontainers.image.description"
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
			LabelService,
			LabelService,
			LabelImageDescription,
			LabelImageDescription,
			LabelImageDescription,
			LabelImageTitle,
			LabelImageTitle,
			LabelImageTitle,
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
