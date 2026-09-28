package assistant

// Capability identifies a composable source of assistant context or tools.
type Capability string

const (
	// CapabilityServiceContext enables service.store RAG retrieval.
	CapabilityServiceContext Capability = "service_context"
	// CapabilityServiceRuntime enables service inspection and service operations.
	CapabilityServiceRuntime Capability = "service_runtime"
	// CapabilityCluster enables Swarm cluster inspection tools.
	CapabilityCluster Capability = "cluster"
	// CapabilityDeploymentHistory enables deployment, event, recommendation, and git history tools.
	CapabilityDeploymentHistory Capability = "deployment_history"
	// CapabilityDeploymentSync enables manual deployment synchronization.
	CapabilityDeploymentSync Capability = "deployment_sync"
	// CapabilityRegistry enables container registry image lookups.
	CapabilityRegistry Capability = "registry"
	// CapabilityExternalRelease enables upstream repository release lookups.
	CapabilityExternalRelease Capability = "external_release"
	// CapabilityDNS enables DNS resolution.
	CapabilityDNS Capability = "dns"
	// CapabilityMetrics enables application metrics lookup.
	CapabilityMetrics Capability = "metrics"
)

// CapabilityProfile describes the context and tools enabled by a capability.
type CapabilityProfile struct {
	Tools          []string
	ServiceContext bool
}

var capabilityProfiles = map[Capability]CapabilityProfile{
	CapabilityServiceContext: {
		ServiceContext: true,
	},
	CapabilityServiceRuntime: {
		Tools: []string{
			"service_logs_get",
			"service_spec_get",
			"service_replicas_set",
			"service_restart_trigger",
			"service_webroute_ping",
			"dependency_graph_get",
		},
	},
	CapabilityCluster: {
		Tools: []string{
			"swarm_node_list",
			"docker_network_list",
			"docker_plugin_list",
			"docker_secret_list",
		},
	},
	CapabilityDeploymentHistory: {
		Tools: []string{
			"history_event_list",
			"recommendation_list",
			"git_commit_list",
			"git_commit_diff",
		},
	},
	CapabilityDeploymentSync: {
		Tools: []string{
			"deploy_sync_trigger",
		},
	},
	CapabilityRegistry: {
		Tools: []string{
			"registry_image_version_get",
		},
	},
	CapabilityExternalRelease: {
		Tools: []string{
			"external_repository_release_latest_get",
		},
	},
	CapabilityDNS: {
		Tools: []string{
			"dns_name_resolve",
		},
	},
	CapabilityMetrics: {
		Tools: []string{
			"self_metrics_list",
		},
	},
}

func capabilityProfile(capability Capability) (CapabilityProfile, bool) {
	profile, ok := capabilityProfiles[capability]
	if !ok {
		return CapabilityProfile{}, false
	}
	profile.Tools = append([]string(nil), profile.Tools...)
	return profile, true
}

func defaultCapabilitiesForRoute(route Route) []Capability {
	switch route {
	case RouteServices:
		return []Capability{
			CapabilityServiceContext,
			CapabilityServiceRuntime,
			CapabilityRegistry,
			CapabilityExternalRelease,
		}
	case RouteCluster:
		return []Capability{CapabilityCluster}
	case RouteDeployments:
		return []Capability{CapabilityDeploymentHistory, CapabilityDeploymentSync}
	case RouteDiagnostics:
		return []Capability{
			CapabilityServiceContext,
			CapabilityServiceRuntime,
			CapabilityDeploymentHistory,
			CapabilityCluster,
			CapabilityRegistry,
			CapabilityExternalRelease,
			CapabilityDNS,
			CapabilityMetrics,
		}
	case RouteLookups:
		return []Capability{
			CapabilityRegistry,
			CapabilityExternalRelease,
			CapabilityDNS,
			CapabilityMetrics,
		}
	default:
		return nil
	}
}

func normalizeCapabilities(capabilities []Capability) ([]Capability, bool) {
	if len(capabilities) == 0 {
		return nil, true
	}
	result := make([]Capability, 0, len(capabilities))
	seen := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if _, ok := capabilityProfiles[capability]; !ok {
			return nil, false
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	return result, true
}

func hasCapability(capabilities []Capability, target Capability) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}
