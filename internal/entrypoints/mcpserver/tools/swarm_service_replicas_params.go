package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type serviceReplicasTarget struct {
	stack   string
	service string
}

func parseServiceReplicasTarget(payload map[string]any) (serviceReplicasTarget, error) {
	stackName, err := parseRequiredStringParam(payload["stack"], "stack")
	if err != nil {
		return serviceReplicasTarget{}, err
	}

	serviceName, err := parseRequiredStringParam(payload["service"], "service")
	if err != nil {
		return serviceReplicasTarget{}, err
	}

	return serviceReplicasTarget{
		stack:   stackName,
		service: serviceName,
	}, nil
}

func parseReplicasParam(raw any) (uint64, error) {
	if raw == nil {
		return 0, fmt.Errorf("replicas is required")
	}

	switch value := raw.(type) {
	case float64:
		if value != math.Trunc(value) {
			return 0, fmt.Errorf("replicas must be integer")
		}
		if value <= 0 {
			return 0, fmt.Errorf("replicas must be > 0")
		}

		return uint64(value), nil
	case int:
		if value <= 0 {
			return 0, fmt.Errorf("replicas must be > 0")
		}

		return uint64(value), nil
	case int64:
		if value <= 0 {
			return 0, fmt.Errorf("replicas must be > 0")
		}

		return uint64(value), nil
	case json.Number:
		parsed, err := strconv.ParseUint(strings.TrimSpace(value.String()), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("replicas must be integer: %w", err)
		}
		if parsed == 0 {
			return 0, fmt.Errorf("replicas must be > 0")
		}
		return parsed, nil
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("replicas must be integer: %w", err)
		}
		if parsed == 0 {
			return 0, fmt.Errorf("replicas must be > 0")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("replicas must be integer")
	}
}

func parseRequiredStringParam(raw any, fieldName string) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("%s is required", fieldName)
	}

	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be string", fieldName)
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}

	return value, nil
}
