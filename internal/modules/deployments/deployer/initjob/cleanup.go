package initjob

import (
	"context"
	"errors"
	"log/slog"

	"github.com/avast/retry-go/v5"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (r *InitJobRunner) enqueueDeleteTask(ctx context.Context, task initJobDeleteTask) {
	select {
	case r.deleteTasks <- task:
	default:
		slog.WarnContext(
			ctx,
			"[initjob] delete queue is full, running cleanup in fallback mode",
			slog.String("service_id", task.serviceID),
			slog.String("service", task.serviceName),
		)
		if err := r.removeServiceWithRetry(ctx, task); err != nil {
			// A cancelled/failed fallback must not lose the cleanup request.
			// The existing worker drains overflow after its current task.
			r.deleteMu.Lock()
			r.deleteOverflow = append(r.deleteOverflow, task)
			r.deleteMu.Unlock()
			// The channel may have emptied while fallback was running, so wake
			// the worker explicitly instead of relying on another incoming job.
			select {
			case r.deleteWake <- struct{}{}:
			default:
			}
		}
	}
}

func (r *InitJobRunner) runDeleteWorker() {
	for {
		select {
		case task, ok := <-r.deleteTasks:
			if !ok {
				return
			}
			_ = r.removeServiceWithRetry(context.Background(), task)
		case <-r.deleteWake:
		}
		for {
			r.deleteMu.Lock()
			if len(r.deleteOverflow) == 0 {
				r.deleteMu.Unlock()
				break
			}
			task := r.deleteOverflow[0]
			r.deleteOverflow[0] = initJobDeleteTask{}
			r.deleteOverflow = r.deleteOverflow[1:]
			r.deleteMu.Unlock()
			_ = r.removeServiceWithRetry(context.Background(), task)
		}
	}
}

func (r *InitJobRunner) removeServiceWithRetry(ctx context.Context, task initJobDeleteTask) error {
	err := retry.New(
		retry.Context(ctx),
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
		return nil
	}

	slog.WarnContext(
		ctx,
		"[initjob] failed to remove job service",
		slog.String("service_id", task.serviceID),
		slog.String("service", task.serviceName),
		slog.Any("err", err),
	)
	return err
}
