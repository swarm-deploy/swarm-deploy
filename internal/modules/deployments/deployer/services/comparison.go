package services

import (
	"slices"
	"strings"

	dockerswarm "github.com/docker/docker/api/types/swarm"
)

// normalizeServiceSets sorts only collections whose order has no execution semantics.
// Commands, DNS servers, placement preferences and mount order remain untouched.
func normalizeServiceSets(spec *dockerswarm.ServiceSpec) {
	if placement := spec.TaskTemplate.Placement; placement != nil {
		slices.Sort(placement.Constraints)
	}
	for i := range spec.TaskTemplate.Networks {
		slices.Sort(spec.TaskTemplate.Networks[i].Aliases)
	}
	slices.SortStableFunc(spec.TaskTemplate.Networks, func(a, b dockerswarm.NetworkAttachmentConfig) int {
		return strings.Compare(a.Target, b.Target)
	})
	if container := spec.TaskTemplate.ContainerSpec; container != nil {
		slices.Sort(container.CapabilityAdd)
		slices.Sort(container.CapabilityDrop)
		// Duplicate environment names can make order significant. Preserve them.
		if uniqueEnvironmentNames(container.Env) {
			slices.Sort(container.Env)
		}
	}
}

func uniqueEnvironmentNames(environment []string) bool {
	seen := make(map[string]bool, len(environment))
	for _, value := range environment {
		name, _, _ := strings.Cut(value, "=")
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}
