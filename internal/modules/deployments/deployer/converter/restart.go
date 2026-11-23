package converter

import (
	"fmt"
	"strconv"
	"strings"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func effectiveRestartPolicy(
	source *compose.ServiceDeployRestartPolicy, legacy string,
) (*dockerswarm.RestartPolicy, error) {
	if source != nil {
		return restartPolicy(source)
	}
	return legacyRestartPolicy(legacy)
}

// legacyRestartPolicy follows CLI v28.3.3's parser and Compose converter, including
// leaving "no" unset and parsing retry suffixes before checking the policy name.
func legacyRestartPolicy(value string) (*dockerswarm.RestartPolicy, error) {
	name, count, colon := strings.Cut(value, ":")
	if colon && name == "" {
		return nil, fmt.Errorf("restart: missing policy before colon")
	}
	attempts := 0
	if count != "" {
		var err error
		attempts, err = strconv.Atoi(count)
		if err != nil {
			return nil, fmt.Errorf("restart: maximum retry count must be an integer")
		}
	}
	switch name {
	case "", "no":
		return nil, nil //nolint:nilnil // An unset policy deliberately delegates to Docker defaults.
	case "always", "unless-stopped":
		return &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionAny}, nil
	case "on-failure":
		// CLI converts its signed integer directly to uint64, without range validation.
		maximum := uint64(attempts)
		return &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionOnFailure, MaxAttempts: &maximum}, nil
	default:
		return nil, fmt.Errorf("restart: unknown policy")
	}
}
