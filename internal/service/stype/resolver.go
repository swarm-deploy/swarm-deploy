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

const (
	// Application is a default service type for business applications.
	Application = "application"
	// Monitoring is a service type for observability and monitoring tools.
	Monitoring = "monitoring"
	// Delivery is a service type for traffic delivery and edge components.
	Delivery = "delivery"
	// ReverseProxy is a service type for reverse proxy components.
	ReverseProxy = "reverseProxy"
	// Database is a service type for data stores.
	Database = "database"
)

// Labels groups metadata labels for type resolving.
type Labels struct {
	// Service contains labels from docker service annotations.
	Service map[string]string
	// Container contains labels from service task container spec.
	Container map[string]string
}

// Resolver resolves service type using labels and image dictionary.
type Resolver struct {
	typeByImageName map[string]string
}

// NewResolver creates type resolver with custom image dictionary.
func NewResolver(typeByImageName map[string]string) *Resolver {
	if len(typeByImageName) == 0 {
		typeByImageName = DefaultByImageName()
	}

	normalized := make(map[string]string, len(typeByImageName))
	for imageName, serviceType := range typeByImageName {
		parsedType, ok := Parse(serviceType)
		if !ok {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(imageName))
		if key == "" {
			continue
		}
		normalized[key] = parsedType
	}

	return &Resolver{
		typeByImageName: normalized,
	}
}

// Resolve resolves service type from labels and image name.
func (r *Resolver) Resolve(image string, labels Labels) string {
	if resolvedType, ok := resolveFromLabels(labels); ok {
		return resolvedType
	}

	imageName := imageNameFromReference(image)
	if imageName != "" {
		if resolvedType, ok := r.typeByImageName[imageName]; ok {
			return resolvedType
		}
	}

	return Application
}

// DefaultByImageName returns built-in type dictionary by normalized image name.
func DefaultByImageName() map[string]string {
	return map[string]string{
		"postgres":      Database,
		"postgresql":    Database,
		"mysql":         Database,
		"mariadb":       Database,
		"mongo":         Database,
		"mongodb":       Database,
		"redis":         Database,
		"valkey":        Database,
		"clickhouse":    Database,
		"elasticsearch": Database,
		"opensearch":    Database,
		"qdrant":        Database,

		"prometheus":    Monitoring,
		"grafana":       Monitoring,
		"loki":          Monitoring,
		"promtail":      Monitoring,
		"tempo":         Monitoring,
		"alertmanager":  Monitoring,
		"cadvisor":      Monitoring,
		"node-exporter": Monitoring,
		"pushgateway":   Monitoring,

		"traefik":      ReverseProxy,
		"nginx":        ReverseProxy,
		"nginx-proxy":  ReverseProxy,
		"haproxy":      ReverseProxy,
		"envoy":        ReverseProxy,
		"caddy":        ReverseProxy,
		"port-forward": ReverseProxy,
		"registry":     Delivery,
		"distribution": Delivery,
	}
}

// Parse validates and normalizes service type.
func Parse(raw string) (string, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case Application:
		return Application, true
	case Monitoring:
		return Monitoring, true
	case Delivery:
		return Delivery, true
	case "reverseproxy", "reverse_proxy", "reverse-proxy":
		return ReverseProxy, true
	case Database:
		return Database, true
	default:
		return "", false
	}
}

func resolveFromLabels(labels Labels) (string, bool) {
	for _, source := range []map[string]string{labels.Service, labels.Container} {
		if source == nil {
			continue
		}

		value := strings.TrimSpace(source[LabelService])
		if value == "" {
			continue
		}

		parsedType, ok := Parse(value)
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
