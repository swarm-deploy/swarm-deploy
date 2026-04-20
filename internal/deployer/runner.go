package deployer

import "context"

type Runner interface {
	// Run executes docker stack deploy command and returns command output.
	Run(ctx context.Context, args ...string) (string, error)
}
