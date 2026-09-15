package history

import "github.com/swarm-deploy/swarm-deploy/internal/event/events"

// FilterEntries filters history entries by optional severity/category/type lists.
// Within each filter list values are matched as OR, between filter lists as AND.
func FilterEntries(
	entries []Entry,
	severities []events.Severity,
	categories []events.Category,
	types []events.TypeName,
) []Entry {
	severitySet := toSeveritySet(severities)
	categorySet := toCategorySet(categories)
	typeSet := toTypeSet(types)
	if len(severitySet) == 0 && len(categorySet) == 0 && len(typeSet) == 0 {
		return append([]Entry(nil), entries...)
	}

	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if len(severitySet) > 0 {
			if _, ok := severitySet[entry.Severity]; !ok {
				continue
			}
		}

		if len(categorySet) > 0 {
			if _, ok := categorySet[entry.Category]; !ok {
				continue
			}
		}

		if len(typeSet) > 0 {
			if _, ok := typeSet[entry.Type.Name()]; !ok {
				continue
			}
		}

		filtered = append(filtered, entry)
	}

	return filtered
}

func toSeveritySet(values []events.Severity) map[events.Severity]struct{} {
	out := make(map[events.Severity]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}

func toCategorySet(values []events.Category) map[events.Category]struct{} {
	out := make(map[events.Category]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}

func toTypeSet(values []events.TypeName) map[events.TypeName]struct{} {
	out := make(map[events.TypeName]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}
