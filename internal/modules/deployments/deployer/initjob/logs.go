package initjob

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (r *InitJobRunner) loadServiceLogs(
	ctx context.Context,
	serviceID string,
	serviceName string,
) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	logsCtx, cancel := context.WithTimeout(ctx, initJobLogCollectionTimeout)
	defer cancel()
	logs, truncated, err := r.swarmService.Services.LogTail(logsCtx, serviceID, initJobLogLimit)
	if err != nil {
		return nil, false, fmt.Errorf("read logs for service %s: %w", serviceName, err)
	}
	return logs, truncated, nil
}

func initJobExecutionStatus(err error) Status {
	if err == nil {
		return Succeeded
	}
	var failed *JobFailedError
	if errors.As(err, &failed) {
		return Failed
	}
	return Interrupted
}

func initJobDiagnosticLogLines(logs []byte) []string {
	text := strings.TrimSpace(string(logs))
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}
