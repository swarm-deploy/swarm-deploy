package swarm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, command string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := strings.TrimSpace(out.String())
	if err != nil {
		if output == "" {
			return "", fmt.Errorf("run %s %v: %w", command, args, err)
		}
		return output, fmt.Errorf("run %s %v: %w: %s", command, args, err, output)
	}

	return output, nil
}
