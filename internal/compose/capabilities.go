package compose

import (
	"slices"
	"strings"
)

// NormalizeCapabilities returns canonical capability additions and removals.
func NormalizeCapabilities(add, drop []string) ([]string, []string) {
	added, dropped := normalizeCapabilityList(add), normalizeCapabilityList(drop)
	dropped = slices.DeleteFunc(dropped, func(value string) bool { return slices.Contains(added, value) })
	return added, dropped
}

func normalizeCapabilityList(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "RESET" {
			continue
		}
		if value == "ALL" {
			return []string{"ALL"}
		}
		if !strings.HasPrefix(value, "CAP_") {
			value = "CAP_" + value
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func normalizeComposeCapabilities(value *Compose) {
	for i := range value.Services {
		service := &value.Services[i]
		service.CapAdd, service.CapDrop = NormalizeCapabilities(service.CapAdd, service.CapDrop)
	}
}
