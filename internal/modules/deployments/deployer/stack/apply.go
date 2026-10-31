// Package stack prepares and applies Docker Compose definitions through the Docker CLI.
package stack

import (
	"context"
	"fmt"
)

const deployArgsExtraCount = 3

// Applier invokes docker stack deploy with a configured binary runner.
type Applier struct {
	runner Runner
	args   []string
}

// NewApplier creates a Docker CLI stack applier.
func NewApplier(runner Runner, args []string) *Applier {
	return &Applier{runner: runner, args: append([]string(nil), args...)}
}

// DefaultDeployArgs returns the detached stack deployment arguments.
func DefaultDeployArgs() []string {
	return []string{"stack", "deploy", "--with-registry-auth", "--detach=true", "--quiet"}
}

// Apply runs docker stack deploy for one Compose file.
func (a *Applier) Apply(ctx context.Context, stackName, composePath string) error {
	args := make([]string, 0, len(a.args)+deployArgsExtraCount)
	args = append(args, a.args...)
	args = append(args, "-c", composePath, stackName)

	if _, err := a.runner.Run(ctx, args...); err != nil {
		return fmt.Errorf("deploy stack %s: %w", stackName, err)
	}
	return nil
}
