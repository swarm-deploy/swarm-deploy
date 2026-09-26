package assistant

import "strings"

var assistantSecurityTools = []string{
	assistantPromptInjectionReportTool,
}

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
			"date",
			"self_metrics_list",
		},
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

func fallbackCapabilityProfile() CapabilityProfile {
	return CapabilityProfile{
		Prompt: strings.TrimSpace(generalPrompt),
		Tools:  append([]string(nil), assistantSecurityTools...),
	}
}
