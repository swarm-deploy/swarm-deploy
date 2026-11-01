package converter

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func healthcheck(source *compose.ServiceHealth) (*container.HealthConfig, error) {
	if source.Disable {
		if len(source.Test.Args) > 0 {
			return nil, fmt.Errorf("healthcheck: test and disable are mutually exclusive")
		}
		return &container.HealthConfig{Test: []string{"NONE"}}, nil
	}
	result := &container.HealthConfig{Test: slices.Clone(source.Test.Args)}
	if len(result.Test) > 0 && !source.Test.IsList() {
		result.Test = append([]string{"CMD-SHELL"}, result.Test...)
	}
	if len(result.Test) > 0 && !slices.Contains([]string{"NONE", "CMD", "CMD-SHELL"}, result.Test[0]) {
		return nil, fmt.Errorf("healthcheck.test: expected NONE, CMD or CMD-SHELL")
	}
	if len(result.Test) > 1 && result.Test[0] == "NONE" {
		return nil, fmt.Errorf("healthcheck.test: NONE cannot have arguments")
	}
	durations := []struct {
		field, value string
		target       *time.Duration
	}{
		{"interval", source.Interval, &result.Interval}, {"timeout", source.Timeout, &result.Timeout},
		{"start_period", source.StartPeriod, &result.StartPeriod},
		{"start_interval", source.StartInterval, &result.StartInterval},
	}
	for _, item := range durations {
		value, err := duration(item.value)
		if err != nil || (value > 0 && value < time.Millisecond) {
			return nil, fmt.Errorf("healthcheck.%s: invalid duration (minimum 1ms)", item.field)
		}
		*item.target = value
	}
	if source.Retries != nil {
		if *source.Retries > math.MaxInt {
			return nil, fmt.Errorf("healthcheck.retries: out of range")
		}
		result.Retries = int(*source.Retries)
	}
	return result, nil
}

func duration(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	result, err := time.ParseDuration(value)
	if err != nil || result < 0 {
		return 0, fmt.Errorf("invalid nonnegative duration")
	}
	return result, nil
}
