package assistant

import (
	"fmt"
	"sort"
	"strings"
)

// capabilitySummary documents capabilities available in this deployment.
// It is informational and does not grant access to any tools.
func (g *graph) capabilitySummary() string {
	registered := make(map[string]struct{})
	for _, definition := range g.tools.Definitions() {
		registered[string(definition.Name)] = struct{}{}
	}

	type group struct {
		label string
		tools []string
	}
	groups := []group{
		{"Service runtime inspection (logs, tasks, specifications, health checks, dependencies)", nil},
		{"Service operations (restart, scaling; confirmation required)", nil},
		{"Docker Swarm cluster (nodes, networks, plugins, secrets)", nil},
		{"Deployments, events, Git history and recommendations", nil},
		{"Alerts", nil},
		{"Deployment synchronization (confirmation required)", nil},
		{"Registry image versions", nil},
		{"External repository releases", nil},
		{"DNS lookups", nil},
		{"Application metrics", nil},
	}

	capabilityGroups := []Capability{
		CapabilityServiceRuntime, CapabilityServiceOperations, CapabilityCluster,
		CapabilityDeploymentHistory, CapabilityAlertManagement, CapabilityDeploymentSync,
		CapabilityRegistry, CapabilityExternalRelease, CapabilityDNS, CapabilityMetrics,
	}
	lines := []string{
		"Available assistant capabilities for this deployment (informational; does not grant tool access):",
	}
	for i, capability := range capabilityGroups {
		profile, ok := capabilityProfile(capability)
		if !ok {
			continue
		}
		for _, toolName := range profile.Tools {
			if _, exists := registered[toolName]; !exists {
				continue
			}
			if len(g.allowedToolSet) > 0 {
				if _, allowed := g.allowedToolSet[toolName]; !allowed {
					continue
				}
			}
			groups[i].tools = append(groups[i].tools, toolName)
		}
		if len(groups[i].tools) == 0 {
			continue
		}
		sort.Strings(groups[i].tools)
		lines = append(lines, fmt.Sprintf("- %s: %s", groups[i].label, strings.Join(groups[i].tools, ", ")))
	}
	if _, ok := capabilityProfile(CapabilityServiceContext); ok {
		lines = append(lines, "- Service catalog metadata and semantic search (when relevant to the request)")
	}
	lines = append(lines,
		"Describe capabilities in user-friendly language, and name tools only if the user asks for tool names.",
		"Tools listed above may be used only when granted by the selected route capabilities and global allowlist.",
		"Registered tools indicate potential access, not that a live cluster was checked or is reachable.",
		"Never claim a tool or live-cluster access is missing merely because the current route does not expose it.",
		"Never claim that a tool call or operation succeeded without its result.",
	)
	return strings.Join(lines, "\n")
}
