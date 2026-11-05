package initjob

import (
	"context"
	"errors"
	"log/slog"

	"github.com/avast/retry-go/v5"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (r *InitJobRunner) enqueueDeleteTask(task initJobDeleteTask) {
	select {
	case r.deleteTasks <- task:
	default:
		slog.WarnContext(
			context.Background(),
			"[initjob] delete queue is full, running cleanup in fallback mode",
			slog.String("service_id", task.serviceID),
			slog.String("service", task.serviceName),
		)
		r.removeServiceWithRetry(context.Background(), task)
	}
}

func (r *InitJobRunner) runDeleteWorker() {
	for task := range r.deleteTasks {
		r.removeServiceWithRetry(context.Background(), task)
	}
}

func (r *InitJobRunner) removeServiceWithRetry(ctx context.Context, task initJobDeleteTask) {
	err := retry.New(
		retry.Attempts(initJobDeleteRetryAttempts),
		retry.Delay(initJobDeleteRetryDelay),
		retry.LastErrorOnly(true),
	).Do(func() error {
		removeCtx, cancel := context.WithTimeout(ctx, initJobDeleteAttemptTimeout)
		defer cancel()

		removeErr := r.swarmService.Services.Remove(removeCtx, task.serviceID)
		if removeErr == nil || errors.Is(removeErr, swarm.ErrServiceNotFound) {
			return nil
		}
		return removeErr
	})
	if err == nil {
		return
	}

	slog.WarnContext(
		ctx,
		"[initjob] failed to remove job service",
		slog.String("service_id", task.serviceID),
		slog.String("service", task.serviceName),
		slog.Any("err", err),
	)
}
