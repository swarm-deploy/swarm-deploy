package initjob

import (
	"context"
	"log/slog"

	"github.com/avast/retry-go/v5"
	cerrdefs "github.com/containerd/errdefs"
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

		removeErr := r.dockerClient.ServiceRemove(removeCtx, task.serviceID)
		if removeErr == nil || cerrdefs.IsNotFound(removeErr) {
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
