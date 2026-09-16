package history

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

// FilterEntries filters history entries by optional severity/category/type lists.
// Within each filter list values are matched as OR, between filter lists as AND.
func FilterEntries(
	entries []Entry,
	severities []events.Severity,
	categories []events.Category,
	types []events.TypeName,
	since *time.Time,
) []Entry {
	severitySet := toSeveritySet(severities)
	categorySet := toCategorySet(categories)
	typeSet := toTypeSet(types)
	if len(severitySet) == 0 && len(categorySet) == 0 && len(typeSet) == 0 && since == nil {
		return append([]Entry(nil), entries...)
	}

	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if matchesEntryFilters(entry, severitySet, categorySet, typeSet, since) {
			filtered = append(filtered, entry)
		}
	}

	return filtered
}

func matchesEntryFilters(
	entry Entry,
	severitySet map[events.Severity]struct{},
	categorySet map[events.Category]struct{},
	typeSet map[events.TypeName]struct{},
	since *time.Time,
) bool {
	if since != nil && entry.CreatedAt.Before(*since) {
		return false
	}

	if len(severitySet) > 0 {
		if _, ok := severitySet[entry.Severity]; !ok {
			return false
		}
	}

	if len(categorySet) > 0 {
		if _, ok := categorySet[entry.Category]; !ok {
			return false
		}
	}

	if len(typeSet) > 0 {
		if _, ok := typeSet[entry.Type.Name()]; !ok {
			return false
		}
	}

	return true
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
