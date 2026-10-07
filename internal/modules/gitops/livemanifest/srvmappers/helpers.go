package srvmappers

import (
	"strconv"
	"strings"
)

const nanoCPUToCPUScale = 1_000_000_000

func ptr[t any](v t) *t {
	return &v
}

func buildObjectRefSource(name string, id string) string {
	source := name
	if source == "" {
		source = id
	} else if id != "" {
		source += ":" + id
	}

	if source == "" {
		return "unknown"
	}

	return source
}

func formatNanoCPUs(nanoCPUs int64) string {
	value := float64(nanoCPUs) / nanoCPUToCPUScale
	formatted := strconv.FormatFloat(value, 'f', 9, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}

	return formatted
}
