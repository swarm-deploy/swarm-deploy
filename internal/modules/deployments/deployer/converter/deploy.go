package converter

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func deployment(spec *dockerswarm.ServiceSpec, source compose.ServiceDeploy) error {
	switch source.Mode {
	case "", "replicated":
		spec.Mode.Replicated = &dockerswarm.ReplicatedService{Replicas: clonePointer(source.Replicas)}
	case "global":
		if source.Replicas != nil {
			return fmt.Errorf("deploy.replicas: incompatible with global mode")
		}
		spec.Mode.Global = &dockerswarm.GlobalService{}
	default:
		return fmt.Errorf("deploy.mode: only replicated and global modes are supported")
	}
	var err error
	if source.Resources != nil {
		spec.TaskTemplate.Resources, err = requirements(source.Resources)
		if err != nil {
			return err
		}
	}
	if source.Placement != nil {
		spec.TaskTemplate.Placement, err = placement(source.Placement)
		if err != nil {
			return err
		}
	}
	if source.RestartPolicy != nil {
		spec.TaskTemplate.RestartPolicy, err = restartPolicy(source.RestartPolicy)
		if err != nil {
			return err
		}
	}
	if source.UpdateConfig != nil {
		u := source.UpdateConfig
		spec.UpdateConfig, err = updatePolicy(
			u.Parallelism, u.Delay, u.FailureAction, u.Monitor, u.MaxFailureRatio, string(u.Order), false,
		)
		if err != nil {
			return fmt.Errorf("deploy.update_config: %w", err)
		}
	}
	if source.RollbackConfig != nil {
		u := source.RollbackConfig
		spec.RollbackConfig, err = updatePolicy(
			u.Parallelism, u.Delay, u.FailureAction, u.Monitor, u.MaxFailureRatio, u.Order, true,
		)
		if err != nil {
			return fmt.Errorf("deploy.rollback_config: %w", err)
		}
	}
	return nil
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func placement(source *compose.ServiceDeployPlacement) (*dockerswarm.Placement, error) {
	result := &dockerswarm.Placement{Constraints: slices.Clone(source.Constraints)}
	if source.MaxReplicasPerNode != nil {
		result.MaxReplicas = *source.MaxReplicasPerNode
	}
	for _, preference := range source.Preferences {
		if preference.Spread == "" {
			return nil, fmt.Errorf("deploy.placement.preferences.spread: required")
		}
		result.Preferences = append(result.Preferences, dockerswarm.PlacementPreference{
			Spread: &dockerswarm.SpreadOver{SpreadDescriptor: preference.Spread},
		})
	}
	return result, nil
}

func restartPolicy(source *compose.ServiceDeployRestartPolicy) (*dockerswarm.RestartPolicy, error) {
	if !slices.Contains([]string{"", "none", "any", "on-failure"}, source.Condition) {
		return nil, fmt.Errorf("deploy.restart_policy.condition: invalid value")
	}
	delay, err := duration(source.Delay)
	if err != nil {
		return nil, fmt.Errorf("deploy.restart_policy.delay: %w", err)
	}
	window, err := duration(source.Window)
	if err != nil {
		return nil, fmt.Errorf("deploy.restart_policy.window: %w", err)
	}
	result := &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyCondition(source.Condition),
		MaxAttempts: clonePointer(source.MaxAttempts)}
	if source.Delay != "" {
		result.Delay = &delay
	}
	if source.Window != "" {
		result.Window = &window
	}
	return result, nil
}

func updatePolicy(
	parallelism *uint64, delay, action, monitor string, ratio *float64, order string, rollback bool,
) (*dockerswarm.UpdateConfig, error) {
	actions := []string{"", "pause", "continue"}
	if !rollback {
		actions = append(actions, "rollback")
	}
	if !slices.Contains(actions, action) {
		return nil, fmt.Errorf("failure_action: invalid value")
	}
	if order != "" && order != "stop-first" && order != "start-first" {
		return nil, fmt.Errorf("order: invalid value")
	}
	result := &dockerswarm.UpdateConfig{Parallelism: 1, FailureAction: action, Order: order}
	if parallelism != nil {
		result.Parallelism = *parallelism
	}
	if ratio != nil {
		if math.IsNaN(*ratio) || *ratio < 0 || *ratio > 1 {
			return nil, fmt.Errorf("max_failure_ratio: must be between 0 and 1")
		}
		result.MaxFailureRatio = float32(*ratio)
	}
	var err error
	result.Delay, err = duration(delay)
	if err != nil {
		return nil, fmt.Errorf("delay: %w", err)
	}
	result.Monitor, err = duration(monitor)
	if err != nil {
		return nil, fmt.Errorf("monitor: %w", err)
	}
	return result, nil
}

func requirements(source *compose.ServiceDeployResources) (*dockerswarm.ResourceRequirements, error) {
	result := &dockerswarm.ResourceRequirements{}
	if source.Limits != nil {
		cpus, memory, err := resourceValues(*source.Limits)
		if err != nil {
			return nil, fmt.Errorf("deploy.resources.limits: %w", err)
		}
		result.Limits = &dockerswarm.Limit{NanoCPUs: cpus, MemoryBytes: memory}
		if source.Limits.Pids != nil {
			if *source.Limits.Pids > math.MaxInt64 {
				return nil, fmt.Errorf("deploy.resources.limits.pids: out of range")
			}
			result.Limits.Pids = int64(*source.Limits.Pids)
		}
	}
	if source.Reservations != nil {
		if source.Reservations.Pids != nil {
			return nil, fmt.Errorf("deploy.resources.reservations.pids: unsupported by swarm")
		}
		cpus, memory, err := resourceValues(*source.Reservations)
		if err != nil {
			return nil, fmt.Errorf("deploy.resources.reservations: %w", err)
		}
		result.Reservations = &dockerswarm.Resources{NanoCPUs: cpus, MemoryBytes: memory}
	}
	return result, nil
}

const nanoCPUs = 1_000_000_000

var decimalCPUs = regexp.MustCompile(`^[+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func resourceValues(source compose.ServiceDeployResource) (int64, int64, error) {
	var cpus int64
	if source.Cpus != "" {
		if !decimalCPUs.MatchString(source.Cpus) {
			return 0, 0, fmt.Errorf("cpus: expected a nonnegative decimal CPU count")
		}
		value, ok := new(big.Rat).SetString(source.Cpus)
		if !ok || value.Sign() < 0 {
			return 0, 0, fmt.Errorf("cpus: invalid nonnegative CPU count")
		}
		value.Mul(value, big.NewRat(nanoCPUs, 1))
		if !value.IsInt() || !value.Num().IsInt64() {
			return 0, 0, fmt.Errorf("cpus: exceeds nanocpu precision or range")
		}
		cpus = value.Num().Int64()
	}
	memory, err := byteSize(source.Memory)
	if err != nil {
		return 0, 0, fmt.Errorf("memory: %w", err)
	}
	return cpus, memory, nil
}
