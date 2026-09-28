package assistant

import "strings"

var assistantSecurityTools = []string{
	assistantPromptInjectionReportTool,
}

// Capability identifies a bounded capability that can augment the primary route.
type Capability string

const (
	// CapabilityRegistryImage adds container-registry image inspection.
	CapabilityRegistryImage Capability = "registry_image"
	// CapabilityExternalRelease adds latest-release inspection for an external repository.
	CapabilityExternalRelease Capability = "external_release"
	// CapabilityDateTime adds current and relative date/time resolution.
	CapabilityDateTime Capability = "date_time"
)

// CapabilityProfile describes the prompt, tools, and context available to a route.
type CapabilityProfile struct {
	// Prompt is route-specific guidance appended to the base system prompt.
	Prompt string
	// Tools is the route-level allowlist of tool names.
	Tools []string
	// ServiceContext determines whether service RAG runs for the route.
	ServiceContext bool
}

var capabilityProfiles = map[Route]CapabilityProfile{
	RouteGeneral: {
		Prompt: generalPrompt,
	},
	RouteOutOfScope: {},
	RoutePlatform: {
		Prompt: platformPrompt,
	},
	RouteServices: {
		Prompt: servicesPrompt,
		Tools: []string{
			"service_logs_get",
			"service_spec_get",
			"service_replicas_set",
			"service_restart_trigger",
			"service_webroute_ping",
			"dependency_graph_get",
			"registry_image_version_get",
		},
		ServiceContext: true,
	},
	RouteCluster: {
		Prompt: clusterPrompt,
		Tools: []string{
			"swarm_node_list",
			"docker_network_list",
			"docker_plugin_list",
			"docker_secret_list",
		},
	},
	RouteDeployments: {
		Prompt: deploymentsPrompt,
		Tools: []string{
			"deploy_sync_trigger",
			"history_event_list",
			"recommendation_list",
			"git_commit_list",
			"git_commit_diff",
		},
	},
	RouteDiagnostics: {
		Prompt: diagnosticsPrompt,
		Tools: []string{
			"history_event_list",
			"swarm_node_list",
			"docker_network_list",
			"service_logs_get",
			"service_spec_get",
			"service_webroute_ping",
			"dependency_graph_get",
			"dns_name_resolve",
			"self_metrics_list",
		},
		ServiceContext: true,
	},
	RouteLookups: {
		Prompt: lookupsPrompt,
		Tools: []string{
			"registry_image_version_get",
			"external_repository_release_latest_get",
			"dns_name_resolve",
			"self_metrics_list",
		},
	},
}

var additionalCapabilityProfiles = map[Capability]CapabilityProfile{
	CapabilityRegistryImage: {
		Prompt:         registryImageCapabilityPrompt,
		Tools:          []string{"registry_image_version_get"},
		ServiceContext: true,
	},
	CapabilityExternalRelease: {
		Prompt:         externalReleaseCapabilityPrompt,
		Tools:          []string{"external_repository_release_latest_get"},
		ServiceContext: true,
	},
	CapabilityDateTime: {
		Tools: []string{"date"},
	},
}

func capabilityProfile(route Route) (CapabilityProfile, bool) {
	profile, ok := capabilityProfiles[route]
	if !ok {
		return CapabilityProfile{}, false
	}

	profile.Prompt = strings.TrimSpace(profile.Prompt)
	profile.Tools = append([]string(nil), profile.Tools...)
	return profile, true
}

func composeCapabilityProfile(route Route, capabilities []Capability) (CapabilityProfile, bool) {
	profile, ok := capabilityProfile(route)
	if !ok {
		return CapabilityProfile{}, false
	}

	prompts := []string{profile.Prompt}
	tools := append([]string(nil), profile.Tools...)
	seenTools := make(map[string]struct{}, len(tools))
	for _, toolName := range tools {
		seenTools[toolName] = struct{}{}
	}

	for _, capability := range capabilities {
		additional, known := additionalCapabilityProfiles[capability]
		if !known {
			continue
		}
		if prompt := strings.TrimSpace(additional.Prompt); prompt != "" {
			prompts = append(prompts, prompt)
		}
		for _, toolName := range additional.Tools {
			if _, seen := seenTools[toolName]; seen {
				continue
			}
			tools = append(tools, toolName)
			seenTools[toolName] = struct{}{}
		}
		profile.ServiceContext = profile.ServiceContext || additional.ServiceContext
	}

	profile.Prompt = strings.Join(prompts, "\n\n")
	profile.Tools = tools
	return profile, true
}

func normalizeCapabilities(capabilities []Capability) []Capability {
	normalized := make([]Capability, 0, len(capabilities))
	seen := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		capability = Capability(strings.ToLower(strings.TrimSpace(string(capability))))
		if _, known := additionalCapabilityProfiles[capability]; !known {
			continue
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		normalized = append(normalized, capability)
		seen[capability] = struct{}{}
	}
	return normalized
}

func fallbackCapabilityProfile() CapabilityProfile {
	return CapabilityProfile{
		Prompt: strings.TrimSpace(generalPrompt),
		Tools:  append([]string(nil), assistantSecurityTools...),
	}
}
