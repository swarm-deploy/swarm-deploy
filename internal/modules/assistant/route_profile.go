package assistant

import "strings"

var assistantUtilityTools = []string{
	"date",
}

var assistantSecurityTools = []string{
	assistantPromptInjectionReportTool,
}

// RouteProfile describes route-specific response guidance.
// Context and tools are selected independently through capabilities.
type RouteProfile struct {
	// Prompt is route-specific guidance appended to the base system prompt.
	Prompt string
}

var routeProfiles = map[Route]RouteProfile{
	RouteGeneral: {
		Prompt: generalPrompt,
	},
	RouteOutOfScope: {},
	RoutePlatform: {
		Prompt: platformPrompt,
	},
	RouteServices: {
		Prompt: servicesPrompt,
	},
	RouteCluster: {
		Prompt: clusterPrompt,
	},
	RouteDeployments: {
		Prompt: deploymentsPrompt,
	},
	RouteDiagnostics: {
		Prompt: diagnosticsPrompt,
	},
	RouteLookups: {
		Prompt: lookupsPrompt,
	},
}

func routeProfile(route Route) (RouteProfile, bool) {
	profile, ok := routeProfiles[route]
	if !ok {
		return RouteProfile{}, false
	}

	profile.Prompt = strings.TrimSpace(profile.Prompt)
	return profile, true
}

func fallbackRouteProfile() RouteProfile {
	return RouteProfile{
		Prompt: strings.TrimSpace(generalPrompt),
	}
}
