package policies

import "github.com/swarm-deploy/swarm-deploy/internal/compose"

// ResolveSelfHeal returns the effective repair policy and the level providing it.
func ResolveSelfHeal(service compose.Service, stack *bool, global bool) (string, bool, error) {
	policy, specified, err := service.SelfHealPolicy()
	if err != nil {
		return "service", false, err
	}
	if specified {
		return "service", policy, nil
	}
	if stack != nil {
		return "stack", *stack, nil
	}

	return "global", global, nil
}
