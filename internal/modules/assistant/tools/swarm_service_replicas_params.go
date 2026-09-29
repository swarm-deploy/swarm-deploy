package tools

import (
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func parseServiceReplicasTarget(stackName string, serviceName string) (swarm.ServiceReference, error) {
	stackName, err := parseRequiredStringParam(stackName, "stack")
	if err != nil {
		return swarm.ServiceReference{}, err
	}

	serviceName, err = parseRequiredStringParam(serviceName, "service")
	if err != nil {
		return swarm.ServiceReference{}, err
	}

	return swarm.NewServiceReference(stackName, serviceName), nil
}

func parseReplicasParam(replicas *uint64) (uint64, error) {
	if replicas == nil {
		return 0, fmt.Errorf("replicas is required")
	}

	if *replicas == 0 {
		return 0, fmt.Errorf("replicas must be > 0")
	}

	return *replicas, nil
}

func parseRequiredStringParam(value string, fieldName string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}

	return value, nil
}
