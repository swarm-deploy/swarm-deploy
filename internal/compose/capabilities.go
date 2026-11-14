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

// Capabilities lists Linux capability names in canonical form: upper-case and without
// the CAP_ prefix. Services decoded from YAML or from persisted JSON snapshots are
// normalized with NormalizeCapabilities; values built in code are used as given.
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
// Unlike most of this package, which preserves source syntax, capabilities are
// canonicalized while decoding, both from Compose YAML and from persisted JSON
// snapshots: their spelling carries no meaning for Docker, and conversion, snapshot
// digests, diffs, deployment history and recommendations must agree on one form.
// Canonical names are upper-case without the CAP_ prefix, as in the Compose
// documentation, so ordinarily written definitions keep their effective digest.
//
// RESET is discarded, ALL replaces the rest of its list, and a capability present
// in both lists is kept only in cap_add. Results are sorted, deduplicated, and nil
// when empty. Normalization never fails: name validation is a separate concern.
// The function is idempotent for valid capability names.
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
// CAP_ALL and CAP_RESET keep their prefix: they are not the ALL and RESET keywords.
func NormalizeCapability(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	name, prefixed := strings.CutPrefix(value, CapabilityPrefix)
	if !prefixed || name == CapabilityAll || name == capabilityReset {
		return value
	}
	return name
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

func normalizeServiceCapabilities(services []Service) {
	for i := range services {
		services[i].CapAdd, services[i].CapDrop = NormalizeCapabilities(services[i].CapAdd, services[i].CapDrop)
	}
}
