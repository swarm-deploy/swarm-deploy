package stype

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
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
	ReverseProxy               Type = "reverseProxy"
	DeploymentManagementSystem Type = "deploymentManagementSystem"
	// Database is a service type for data stores.
	Database      Type = "database"
	SecretManager Type = "secretManager"
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

	imageName := utils.ImageName(image)
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
	case string(SecretManager):
		return SecretManager, true
	case string(DeploymentManagementSystem):
		return DeploymentManagementSystem, true
	default:
		return "", false
	}
}

func resolveFromLabels(labels Labels) (Type, bool) {
	for _, source := range []map[string]string{labels.Service, labels.Container} {
		if source == nil {
			continue
		}

		value := strings.TrimSpace(source[labelsdict.ServiceType])
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
