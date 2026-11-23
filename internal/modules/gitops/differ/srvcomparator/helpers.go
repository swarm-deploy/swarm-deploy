package srvcomparator

func mapKeys[T any](source map[string]T) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	return keys
}

func boolScore(value bool) int {
	if value {
		return 1
	}
	return 0
}
