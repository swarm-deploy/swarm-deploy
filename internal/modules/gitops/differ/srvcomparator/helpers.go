package srvcomparator

import "encoding/json"

func boolScore(value bool) int {
	if value {
		return 1
	}
	return 0
}

func stableJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}

	return string(encoded)
}
