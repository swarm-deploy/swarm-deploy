package compose

import (
	"slices"
	"strings"
)

const (
	// CapabilityAll adds or drops every Linux capability.
	CapabilityAll = "ALL"
	// CapabilityPrefix is the Linux name prefix that Compose allows to omit.
	CapabilityPrefix = "CAP_"

	capabilityReset = "RESET"
)

// Capabilities lists Linux capability names in canonical form (see NormalizeCapability):
// ordinary names are upper-case and without the CAP_ prefix. Services decoded from YAML
// or from persisted JSON snapshots are normalized; values built in code are used as given.
type Capabilities []string

// DockerNames returns the names in the CAP_ form expected by the Docker API.
// ALL keeps its name. The result is sorted, deduplicated, and nil when empty.
func (c Capabilities) DockerNames() []string {
	if len(c) == 0 {
		return nil
	}
	result := make([]string, 0, len(c))
	for _, value := range c {
		if value != CapabilityAll && !strings.HasPrefix(value, CapabilityPrefix) {
			value = CapabilityPrefix + value
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// NormalizeCapabilities returns canonical cap_add and cap_drop lists.
//
// Like command and healthcheck syntax, capabilities are canonicalized while decoding,
// both from Compose YAML and from persisted JSON snapshots (see normalizeServices):
// their spelling carries no meaning for Docker, and conversion, snapshot digests,
// diffs, deployment history and recommendations must agree on one form.
// Canonical names are upper-case without the CAP_ prefix, as in the Compose
// documentation, so ordinarily written definitions keep their effective digest.
//
// RESET is discarded, ALL replaces the rest of its list, and a capability present
// in both lists is kept only in cap_add. Results are sorted, deduplicated, and nil
// when empty. Normalization never fails: name validation is a separate concern.
// The function is idempotent, and DockerNames of its result equals what direct
// conversion of the source values would produce.
func NormalizeCapabilities(add, drop Capabilities) (Capabilities, Capabilities) {
	added := normalizeCapabilityList(add)
	dropped := slices.DeleteFunc(normalizeCapabilityList(drop), func(value string) bool {
		return slices.Contains(added, value)
	})
	if len(dropped) == 0 {
		dropped = nil
	}
	return added, dropped
}

// NormalizeCapability returns one capability name in canonical form.
//
// Ordinary names (A-Z, 0-9 and _) are stored without the CAP_ prefix. Any other
// value keeps exactly the name Docker would receive, prefix included, so typos
// such as CAP_CAP_SYS_ADMIN or "CAP_ SYS_ADMIN" are still rejected by Docker
// instead of collapsing into a valid capability on repeated normalization.
// Canonical and Docker names correspond one-to-one, which keeps it idempotent.
func NormalizeCapability(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == CapabilityAll || value == capabilityReset {
		return value
	}
	docker := value
	if !strings.HasPrefix(docker, CapabilityPrefix) {
		docker = CapabilityPrefix + docker
	}
	if name := strings.TrimPrefix(docker, CapabilityPrefix); isPlainCapabilityName(name) {
		return name
	}
	return docker
}

// isPlainCapabilityName reports whether a name can be stored without CAP_ and
// unambiguously restored: it is not a keyword and does not start with CAP_ itself.
func isPlainCapabilityName(name string) bool {
	if name == "" || name == CapabilityAll || name == capabilityReset || strings.HasPrefix(name, CapabilityPrefix) {
		return false
	}
	for _, r := range name {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func normalizeCapabilityList(values Capabilities) Capabilities {
	var result Capabilities
	for _, value := range values {
		value = NormalizeCapability(value)
		switch value {
		case capabilityReset:
			continue
		case CapabilityAll:
			return Capabilities{CapabilityAll}
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}
