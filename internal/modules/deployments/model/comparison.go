package model

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

const (
	ignoredOptionPathParts = 4
	configsField           = "configs"
	secretsField           = "secrets"
)

func ignoredComparisonPath(path string) bool {
	parts := strings.Split(path, "/")
	last := len(parts) - 1
	if last > 0 && parts[last] == "IsList" {
		switch parts[last-1] {
		case "command", "entrypoint", "test":
			return true
		}
	}
	if last > 1 && parts[last] == "short_form" && (parts[last-2] == secretsField || parts[last-2] == configsField) {
		return true
	}
	return len(parts) >= ignoredOptionPathParts && parts[0] == "services" && parts[2] == "Extra" &&
		compose.IsSwarmIgnoredOption(parts[3])
}
