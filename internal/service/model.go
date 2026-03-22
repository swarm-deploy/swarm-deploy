package service

import "strings"

// Type is a service classification.
type Type string

const (
	// TypeApplication is a default service type for business applications.
	TypeApplication Type = "application"
	// TypeMonitoring is a service type for observability and monitoring tools.
	TypeMonitoring Type = "monitoring"
	// TypeDelivery is a service type for traffic delivery and edge components.
	TypeDelivery Type = "delivery"
	// TypeReverseProxy is a service type for edge reverse proxy components.
	TypeReverseProxy Type = "reverseProxy"
	// TypeDatabase is a service type for data stores.
	TypeDatabase Type = "database"
)

// ParseType validates and normalizes service type.
func ParseType(raw string) (Type, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case string(TypeApplication):
		return TypeApplication, true
	case string(TypeMonitoring):
		return TypeMonitoring, true
	case string(TypeDelivery):
		return TypeDelivery, true
	case "reverseproxy", "reverse_proxy", "reverse-proxy":
		return TypeReverseProxy, true
	case string(TypeDatabase):
		return TypeDatabase, true
	default:
		return "", false
	}
}

// Info is a persisted service metadata record.
type Info struct {
	// Name is a service name inside stack.
	Name string `json:"name"`
	// Stack is a docker stack name.
	Stack string `json:"stack"`
	// Description is a human-readable service description.
	Description string `json:"description,omitempty"`
	// Type is a service classification.
	Type Type `json:"type"`
	// Image is a service container image reference.
	Image string `json:"image"`
}
