package stype

import (
	"strings"

	"github.com/distribution/reference"
)

const (
	// LabelService is a service/container label with service type value.
	LabelService       = "org.swarm-deploy.service.type"
	imageRefSplitParts = 2
)

// Type is a service classification.
type Type string

const (
	// Application is a default service type for business applications.
	Application Type = "application"
	// Monitoring is a service type for observability and monitoring tools.
	Monitoring Type = "monitoring"
	// Delivery is a service type for traffic delivery and edge components.
	Delivery Type = "delivery"
	// ReverseProxy is a service type for reverse proxy components.
	ReverseProxy Type = "reverseProxy"
	// Database is a service type for data stores.
	Database Type = "database"
)

// Labels groups metadata labels for type resolving.
type Labels struct {
	// Service contains labels from docker service annotations.
	Service map[string]string
	// Container contains labels from service task container spec.
	Container map[string]string
}

// Resolver resolves service type using labels and image dictionary.
type Resolver struct{}

// NewResolver creates type resolver with custom image dictionary.
func NewResolver() *Resolver {
	return &Resolver{}
}

// Resolve resolves service type from labels and image name.
func (r *Resolver) Resolve(image string, labels Labels) Type {
	if resolvedType, ok := resolveFromLabels(labels); ok {
		return resolvedType
	}

	imageName := imageNameFromReference(image)
	if imageName != "" {
		if resolvedType, ok := imageTypeDict[imageName]; ok {
			return resolvedType
		}
	}

	return Application
}

// NormalizeTypeName validates and normalizes service type.
func NormalizeTypeName(raw string) (Type, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case string(Application):
		return Application, true
	case string(Monitoring):
		return Monitoring, true
	case string(Delivery):
		return Delivery, true
	case "reverseproxy", "reverse_proxy", "reverse-proxy":
		return ReverseProxy, true
	case string(Database):
		return Database, true
	default:
		return "", false
	}
}

func resolveFromLabels(labels Labels) (Type, bool) {
	for _, source := range []map[string]string{labels.Service, labels.Container} {
		if source == nil {
			continue
		}

		value := strings.TrimSpace(source[LabelService])
		if value == "" {
			continue
		}

		parsedType, ok := NormalizeTypeName(value)
		if ok {
			return parsedType, true
		}
	}

	return "", false
}

func imageNameFromReference(image string) string {
	trimmedImage := strings.TrimSpace(image)
	if trimmedImage == "" {
		return ""
	}

	trimmedImage = strings.SplitN(trimmedImage, "@", imageRefSplitParts)[0]
	parsedImage, err := reference.ParseNormalizedNamed(trimmedImage)
	if err == nil {
		namePath := reference.Path(parsedImage)
		if slashIdx := strings.LastIndex(namePath, "/"); slashIdx >= 0 {
			namePath = namePath[slashIdx+1:]
		}
		return strings.ToLower(strings.TrimSpace(namePath))
	}

	if slashIdx := strings.LastIndex(trimmedImage, "/"); slashIdx >= 0 {
		trimmedImage = trimmedImage[slashIdx+1:]
	}
	if colonIdx := strings.LastIndex(trimmedImage, ":"); colonIdx >= 0 {
		trimmedImage = trimmedImage[:colonIdx]
	}
	return strings.ToLower(strings.TrimSpace(trimmedImage))
}
