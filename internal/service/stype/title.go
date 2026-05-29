package stype

// Title returns a short human-readable title for a service type.
func Title(typ Type) string {
	switch typ {
	case Application:
		return "Application"
	case Monitoring:
		return "Monitoring"
	case Delivery:
		return "Delivery"
	case ReverseProxy:
		return "Reverse Proxy"
	case Database:
		return "Database"
	case SecretManager:
		return "Secret Manager"
	case DeploymentManagementSystem:
		return "Deployment Management System"
	default:
		return "Application"
	}
}
