package deployer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	taskLogCollectionTimeout = 5 * time.Second
	maxFailedTaskLogs        = 3
)

func (d *Deployer) withConvergenceTaskLogs(ctx context.Context, result waiter.ServiceWaitResult, cause error) error {
	if !errors.Is(cause, waiter.ErrConvergenceFailed) {
		return cause
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w\nTask logs unavailable: %s", cause, taskLogFailure(ctx, ctx.Err()))
	}
	if len(result.FailedTasks) == 0 {
		return fmt.Errorf("%w\nTask logs unavailable: failed task could not be identified", cause)
	}

	// One deadline covers requests and stream consumption for all failed services.
	logsCtx, cancel := context.WithTimeout(ctx, taskLogCollectionTimeout)
	defer cancel()
	remainingLines, remainingBytes := faults.MaxDiagnosticLogLines, faults.MaxDiagnosticLogBytes
	for index, failure := range result.FailedTasks {
		if index >= maxFailedTaskLogs || remainingLines <= 0 || remainingBytes <= 0 {
			return fmt.Errorf("%w\nTask logs unavailable for %d additional failed services: collection limit reached",
				cause, len(result.FailedTasks)-index)
		}
		lines, logErr := d.collectTaskLogs(logsCtx, failure.FailedTaskID, remainingLines, remainingBytes)
		unavailable := ""
		if logErr != nil {
			unavailable = taskLogFailure(ctx, logErr)
		} else if len(lines) == 0 {
			unavailable = "no logs found (or no safe log lines available)"
		}
		content := strings.Join(lines, "\n")
		remainingLines -= len(lines)
		remainingBytes -= len(content)
		if len(lines) > 0 {
			remainingBytes-- // Reserve a separator between services' log content.
		}
		if unavailable != "" {
			content += "\nTask logs unavailable: " + unavailable
		}
		cause = fmt.Errorf("%w\nservice %s task %s logs:\n%s", cause, failure.Name, failure.FailedTaskID,
			strings.TrimSpace(content))
	}
	return cause
}

func (d *Deployer) collectTaskLogs(
	ctx context.Context, taskID string, maxLines, maxBytes int,
) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// TaskLogs includes both stdout and stderr and normalizes Docker's framing.
	entries, errs, err := d.services.TaskLogs(ctx, taskID, swarm.TaskLogsOptions{
		Follow: false, Limit: faults.MaxDiagnosticLogLines,
	})
	if err != nil {
		return nil, err
	}
	var lines []string
	var messages []string
	received := 0
	for entries != nil || errs != nil {
		select {
		case <-ctx.Done():
			return lines, ctx.Err()
		case entry, ok := <-entries:
			if !ok {
				entries = nil
				continue
			}
			received++
			// Filter the bounded batch together so split private key blocks stay excluded.
			messages = append(messages, entry.Message)
			lines = faults.DiagnosticLogLines(messages, maxLines, maxBytes)
			used := len(strings.Join(lines, "\n"))
			if received >= faults.MaxDiagnosticLogLines || len(lines) >= maxLines || used >= maxBytes {
				return lines, nil
			}
		case streamErr, ok := <-errs:
			if !ok {
				errs = nil
			} else if streamErr != nil {
				return lines, streamErr
			}
		}
	}
	return lines, nil
}

func taskLogFailure(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "parent context deadline exceeded"
	case ctx.Err() != nil:
		return "parent context canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "retrieval timed out after 5s"
	default:
		return "Docker API error: " + faults.SafeDiagnostic(err)
	}
}
