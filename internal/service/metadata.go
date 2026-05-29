package service

import (
	serviceDescription "github.com/swarm-deploy/swarm-deploy/internal/service/description"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/service/stype"
)

const (
	labelServiceType = serviceType.LabelService
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

// Metadata is resolved service metadata.
type Metadata struct {
	// Description is a human-readable service description.
	Description string
	// Type is a normalized service classification.
	Type serviceType.Type
}

// MetadataExtractor resolves service metadata using labels and image dictionary.
type MetadataExtractor struct {
	descriptionResolve *serviceDescription.Resolver
	typeResolve        *serviceType.Resolver
}

// NewMetadataExtractor creates metadata extractor with custom image dictionary.
func NewMetadataExtractor() *MetadataExtractor {
	return &MetadataExtractor{
		descriptionResolve: serviceDescription.NewResolver(),
		typeResolve:        serviceType.NewResolver(),
	}
}

// Resolve resolves service description and type from labels and image name.
func (r *MetadataExtractor) Resolve(image string, labels Labels) Metadata {
	return Metadata{
		Description: r.resolveDescription(labels),
		Type:        r.resolveType(image, labels),
	}
}

func (r *MetadataExtractor) resolveType(image string, labels Labels) serviceType.Type {
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

func (r *MetadataExtractor) resolveDescription(labels Labels) string {
	return r.descriptionResolve.Resolve(serviceDescription.Labels{
		Service:   labels.Service,
		Container: labels.Container,
		Image:     labels.Image,
	})
}
