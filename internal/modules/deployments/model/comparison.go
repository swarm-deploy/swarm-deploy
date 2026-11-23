package model

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

const ignoredOptionPathParts = 4

// ignoredComparisonPath reports metadata that baselines persisted by older
// releases can still contain: the command form flag and Swarm-ignored options.
func ignoredComparisonPath(path string) bool {
	parts := strings.Split(path, "/")
	last := len(parts) - 1
	if last > 0 && parts[last] == "IsList" {
		switch parts[last-1] {
		case "command", "entrypoint", "test":
			return true
		}
	}
	return len(parts) >= ignoredOptionPathParts && parts[0] == "services" && parts[2] == "Extra" &&
		compose.IsSwarmIgnoredOption(parts[3])
}
