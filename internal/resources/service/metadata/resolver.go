package metadata

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

// DescriptionResolver resolves service description using labels priority.
type DescriptionResolver struct {
	keys []string
}

// NewDescriptionResolver creates description resolver.
func NewDescriptionResolver() *DescriptionResolver {
	return &DescriptionResolver{
		keys: []string{
			labelsdict.ServiceDescription,
			labelsdict.ServiceDescription,
			labelsdict.OCIImageDescription,
			labelsdict.OCIImageDescription,
			labelsdict.OCIImageDescription,
			labelsdict.OCIImageTitle,
			labelsdict.OCIImageTitle,
			labelsdict.OCIImageTitle,
		},
	}
}

// Resolve resolves service description from labels by priority.
func (r *DescriptionResolver) Resolve(labels Labels, meta *Metadata) {
	meta.Description = r.resolveDescription(labels)
}

func (r *DescriptionResolver) resolveDescription(labels Labels) string {
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
		r.keys,
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
