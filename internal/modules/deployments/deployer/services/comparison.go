package services

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/distribution/reference"

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

// SpecsEqual compares managed desired fields, allowing Docker defaults and runtime-owned values.
// Invalid ownership policies never match.
func SpecsEqual(current, desired dockerswarm.ServiceSpec) bool {
	managed, err := ReplicasManaged(desired)
	if err != nil {
		return false
	}
	current = cloneSpec(current)
	desired = cloneSpec(desired)
	if !managed {
		preserveReplicas(current, &desired)
	}
	desired.TaskTemplate.ForceUpdate = current.TaskTemplate.ForceUpdate
	normalizeImageResolution(&current, &desired)
	normalizeDockerDefaults(&current)
	normalizeDockerDefaults(&desired)
	normalizeServiceSets(&current)
	normalizeServiceSets(&desired)
	return reflect.DeepEqual(canonicalSpec(current), canonicalSpec(desired))
}

func cloneSpec(spec dockerswarm.ServiceSpec) dockerswarm.ServiceSpec {
	payload, _ := json.Marshal(spec)
	var cloned dockerswarm.ServiceSpec
	_ = json.Unmarshal(payload, &cloned)
	return cloned
}

// normalizeImageResolution reuses the resolved image only when the actual reference
// can be the SDK's resolution of the unchanged Compose reference. Labels alone
// are insufficient evidence: repository, tag and explicit digest must agree.
func normalizeImageResolution(current, desired *dockerswarm.ServiceSpec) bool {
	if current.TaskTemplate.ContainerSpec == nil || desired.TaskTemplate.ContainerSpec == nil {
		return false
	}
	wanted := desired.TaskTemplate.ContainerSpec.Image
	actual := current.TaskTemplate.ContainerSpec.Image
	if current.Labels[stackImageLabel] == "" || current.Labels[stackImageLabel] != desired.Labels[stackImageLabel] ||
		!resolvedImageMatches(actual, wanted) {
		return false
	}
	desired.TaskTemplate.ContainerSpec.Image = actual
	if current.TaskTemplate.Placement != nil && len(current.TaskTemplate.Placement.Platforms) > 0 {
		if desired.TaskTemplate.Placement == nil {
			desired.TaskTemplate.Placement = &dockerswarm.Placement{}
		}
		desired.TaskTemplate.Placement.Platforms = slices.Clone(current.TaskTemplate.Placement.Platforms)
	}
	return true
}

func resolvedImageMatches(actual, wanted string) bool {
	current, err := reference.ParseNormalizedNamed(actual)
	if err != nil {
		return false
	}
	desired, err := reference.ParseNormalizedNamed(wanted)
	if err != nil || current.Name() != desired.Name() {
		return false
	}
	if pinned, ok := desired.(reference.Canonical); ok {
		resolved, canonical := current.(reference.Canonical)
		return canonical && pinned.Digest() == resolved.Digest()
	}
	desired = reference.TagNameOnly(desired)
	current = reference.TagNameOnly(current)
	desiredTag, tagged := desired.(reference.Tagged)
	currentTag, currentTagged := current.(reference.Tagged)
	return tagged && currentTagged && desiredTag.Tag() == currentTag.Tag()
}

func digestPinned(spec dockerswarm.ServiceSpec) bool {
	if spec.TaskTemplate.ContainerSpec == nil {
		return false
	}
	named, err := reference.ParseNormalizedNamed(spec.TaskTemplate.ContainerSpec.Image)
	if err != nil {
		return false
	}
	_, pinned := named.(reference.Canonical)
	return pinned
}

func canonicalSpec(spec dockerswarm.ServiceSpec) any {
	payload, _ := json.Marshal(spec)
	var value any
	_ = json.Unmarshal(payload, &value)
	return pruneEmpty(value)
}

func pruneEmpty(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			child = pruneEmpty(child)
			if child == nil || isEmptyCollection(child) {
				delete(typed, key)
				continue
			}
			typed[key] = child
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = pruneEmpty(typed[index])
		}
		return typed
	default:
		return value
	}
}

func isEmptyCollection(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	default:
		return false
	}
}
