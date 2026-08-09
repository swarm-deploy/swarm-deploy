package metadata

import (
	serviceDescription "github.com/swarm-deploy/swarm-deploy/internal/resources/service/description"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/resources/service/stype"
)

// Metadata is resolved service metadata.
type Metadata struct {
	// Description is a human-readable service description.
	Description string
	// Type is a normalized service classification.
	Type          serviceType.Type
	RepositoryURL string
}

// Extractor resolves service metadata using labels and image dictionary.
type Extractor struct {
	descriptionResolve *serviceDescription.Resolver
	typeResolve        *serviceType.Resolver

	resolvers []LabelResolver
}

// NewExtractor creates metadata extractor with custom image dictionary.
func NewExtractor() *Extractor {
	return &Extractor{
		descriptionResolve: serviceDescription.NewResolver(),
		typeResolve:        serviceType.NewResolver(),
		resolvers: []LabelResolver{
			NewRepositoryResolver(),
		},
	}
}

// Extract resolves service description and type from labels and image name.
func (r *Extractor) Extract(image string, labels Labels) Metadata {
	meta := &Metadata{
		Description: r.resolveDescription(labels),
		Type:        r.resolveType(image, labels),
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

func (r *Extractor) resolveDescription(labels Labels) string {
	return r.descriptionResolve.Resolve(serviceDescription.Labels{
		Service:   labels.Service,
		Container: labels.Container,
		Image:     labels.Image,
	})
}
