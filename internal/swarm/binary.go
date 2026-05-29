package swarm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type BinaryRunner struct {
	command string
}

func newBinaryRunner(command string) *BinaryRunner {
	return &BinaryRunner{
		command: command,
	}
}

func (r *BinaryRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.command, args...) //nolint:gosec // command got from configuration
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	output := strings.TrimSpace(out.String())
	if err != nil {
		if output == "" {
			return "", fmt.Errorf("run %s %v: %w", r.command, args, err)
		}
		return output, fmt.Errorf("run %s %v: %w: %s", r.command, args, err, output)
	}

	return output, nil
}
