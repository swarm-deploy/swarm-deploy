package metadata

import (
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/resources/service/stype"
)

// Metadata is resolved service metadata.
type Metadata struct {
	// Description is a human-readable service description.
	Description string `json:"description"`
	// Type is a normalized service classification.
	Type serviceType.Type `json:"type"`
	// RepositoryURL is a source repository URL resolved from service labels.
	RepositoryURL string `json:"repository_url"`
	// Links is a list of additional service-related links resolved from service labels.
	Links []Link `json:"links"`
}

// Link is an additional service-related URL.
type Link struct {
	// Type is a human-readable link type.
	Type string `json:"type"`
	// URL is a link target.
	URL string `json:"url"`
}

// Extractor resolves service metadata using labels and image dictionary.
type Extractor struct {
	typeResolve *serviceType.Resolver

	resolvers []LabelResolver
}

// NewExtractor creates metadata extractor with custom image dictionary.
func NewExtractor() *Extractor {
	return &Extractor{
		typeResolve: serviceType.NewResolver(),
		resolvers: []LabelResolver{
			NewDescriptionResolver(),
			NewRepositoryResolver(),
			NewLinksResolver(),
		},
	}
}

// Extract resolves service description and type from labels and image name.
func (r *Extractor) Extract(image string, labels Labels) Metadata {
	meta := &Metadata{
		Type: r.resolveType(image, labels),
	}

	for _, resolver := range r.resolvers {
		resolver.Resolve(labels, meta)
	}

	return *meta
}

func (r *Extractor) resolveType(image string, labels Labels) serviceType.Type {
	resolvedTypeLabel := r.typeResolve.Resolve(image, serviceType.Labels{
		Service:   labels.Service,
		Container: labels.Container,
	})
	resolvedType, ok := serviceType.NormalizeTypeName(string(resolvedTypeLabel))
	if !ok {
		return serviceType.Application
	}
	return resolvedType
}
