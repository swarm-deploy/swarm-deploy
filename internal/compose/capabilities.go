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

// NormalizeCapabilities returns canonical cap_add and cap_drop lists.
//
// Unlike most of this package, which preserves source syntax, capabilities are
// canonicalized while parsing: their spelling carries no meaning for Docker, and
// conversion, snapshot digests, diffs and recommendations must agree on one form.
// Canonical names are upper-case without the CAP_ prefix, as in the Compose
// documentation, so ordinarily written definitions keep their effective digest.
//
// RESET is discarded, ALL replaces the rest of its list, and a capability present
// in both lists is kept only in cap_add. Results are sorted, deduplicated, and nil
// when empty. Normalization never fails: name validation is a separate concern.
// The function is idempotent for valid capability names.
func NormalizeCapabilities(add, drop []string) ([]string, []string) {
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

func normalizeCapabilityList(values []string) []string {
	var result []string
	for _, value := range values {
		value = NormalizeCapability(value)
		switch value {
		case capabilityReset:
			continue
		case CapabilityAll:
			return []string{CapabilityAll}
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}
