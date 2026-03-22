package service

import (
	serviceDescription "github.com/artarts36/swarm-deploy/internal/service/description"
	serviceType "github.com/artarts36/swarm-deploy/internal/service/stype"
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
	Type Type
}

// Resolver resolves service metadata using labels and image dictionary.
type Resolver struct {
	typeByImageName    map[string]Type
	descriptionResolve *serviceDescription.Resolver
	typeResolve        *serviceType.Resolver
}

// NewResolver creates metadata resolver with custom image dictionary.
func NewResolver(typeByImageName map[string]Type) *Resolver {
	if len(typeByImageName) == 0 {
		typeByImageName = DefaultTypeByImageName()
	}

	normalizedTypes := make(map[string]Type, len(typeByImageName))
	normalizedTypeLabels := make(map[string]string, len(typeByImageName))
	for imageName, serviceType := range typeByImageName {
		parsedType, ok := ParseType(string(serviceType))
		if !ok {
			continue
		}
		normalizedTypes[imageName] = parsedType
		normalizedTypeLabels[imageName] = string(parsedType)
	}

	return &Resolver{
		typeByImageName:    normalizedTypes,
		descriptionResolve: serviceDescription.NewResolver(),
		typeResolve:        serviceType.NewResolver(normalizedTypeLabels),
	}
}

// Resolve resolves service description and type from labels and image name.
func (r *Resolver) Resolve(image string, labels Labels) Metadata {
	return Metadata{
		Description: r.resolveDescription(labels),
		Type:        r.resolveType(image, labels),
	}
}

// DefaultTypeByImageName returns default service type dictionary.
func DefaultTypeByImageName() map[string]Type {
	defaultTypeLabels := serviceType.DefaultByImageName()
	result := make(map[string]Type, len(defaultTypeLabels))
	for imageName, typeValue := range defaultTypeLabels {
		parsedType, ok := ParseType(typeValue)
		if !ok {
			continue
		}
		result[imageName] = parsedType
	}
	return result
}

func (r *Resolver) resolveType(image string, labels Labels) Type {
	if r.typeResolve == nil {
		typeByImageName := make(map[string]string, len(r.typeByImageName))
		for imageName, serviceType := range r.typeByImageName {
			typeByImageName[imageName] = string(serviceType)
		}
		r.typeResolve = serviceType.NewResolver(typeByImageName)
	}

	resolvedTypeLabel := r.typeResolve.Resolve(image, serviceType.Labels{
		Service:   labels.Service,
		Container: labels.Container,
	})
	resolvedType, ok := ParseType(resolvedTypeLabel)
	if !ok {
		return TypeApplication
	}
	return resolvedType
}

func (r *Resolver) resolveDescription(labels Labels) string {
	if r.descriptionResolve == nil {
		r.descriptionResolve = serviceDescription.NewResolver()
	}

	return r.descriptionResolve.Resolve(serviceDescription.Labels{
		Service:   labels.Service,
		Container: labels.Container,
		Image:     labels.Image,
	})
}
