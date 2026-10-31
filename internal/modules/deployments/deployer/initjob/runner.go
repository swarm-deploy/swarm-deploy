// Package initjob executes temporary Docker services declared as deployment Init Jobs.
package initjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	initJobDeleteTasksBuffer    = 128
	initJobDeleteRetryAttempts  = uint(5)
	initJobDeleteRetryDelay     = 1 * time.Second
	initJobDeleteAttemptTimeout = 10 * time.Second
	initJobObjectFileMode       = 0o444
	initJobLogCollectionTimeout = 5 * time.Second
	initJobLogLimit             = 64 * 1024
)

type initJobDeleteTask struct {
	serviceID   string
	serviceName string
}

// InitJobRunner creates and tracks init jobs for stack services.
type InitJobRunner struct {
	dockerClient *client.Client
	swarmService *swarm.Swarm
	authManager  registry.AuthManager
	metrics      Metrics

	pollInterval time.Duration
	timeout      time.Duration

	deleteTasks chan initJobDeleteTask
}

// NewInitJobRunner creates init job runner with async cleanup queue.
func NewInitJobRunner(
	dockerClient *client.Client,
	swarmService *swarm.Swarm,
	pollInterval time.Duration,
	timeout time.Duration,
	metrics Metrics,
) *InitJobRunner {
	runner := &InitJobRunner{
		dockerClient: dockerClient,
		swarmService: swarmService,
		authManager:  registry.NewAuthManager(),
		metrics:      metrics,
		pollInterval: pollInterval,
		timeout:      timeout,
		deleteTasks:  make(chan initJobDeleteTask, initJobDeleteTasksBuffer),
	}

	go runner.runDeleteWorker()

	return runner
}

// Run executes a single init job, waits until completion and collects bounded Docker output.
func (r *InitJobRunner) Run(
	ctx context.Context,
	spec Spec,
) (Result, error) {
	if spec.Job.Image == "" {
		return Result{}, errors.New("init job image is required")
	}

	r.metrics.RecordInitJobRun(spec.StackName, spec.ServiceName)

	timeout := r.timeout
	if spec.Job.Timeout.Value > 0 {
		timeout = spec.Job.Timeout.Value
	}

	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	jobName := BuildName(spec.StackName, spec.ServiceName, spec.Job.Name)
	serviceRef := swarm.NewServiceReference(spec.StackName, jobName)

	serviceSpec, err := r.buildInitServiceSpec(jobCtx, spec, serviceRef.Name())
	if err != nil {
		return Result{}, err
	}

	serviceCreateOptions, err := r.buildInitServiceCreateOptions(spec.Job.Image)
	if err != nil {
		return Result{}, fmt.Errorf("build init job service create options: %w", err)
	}

	slog.InfoContext(ctx, "[initjob] running job",
		slog.String("stack.name", spec.StackName),
		slog.String("service.name", spec.ServiceName),
		slog.String("initjob.name", spec.Job.Name),
		slog.String("initjob.image", spec.Job.Image),
		slog.Any("initjob.networks", serviceSpec.TaskTemplate.Networks),
		slog.String("initjob.timeout", spec.Job.Timeout.Value.String()),
	)

	startedAt := time.Now().UTC()
	serviceCreate, err := r.dockerClient.ServiceCreate(jobCtx, serviceSpec, serviceCreateOptions)
	if err != nil {
		return Result{}, fmt.Errorf("create init job service %s: %w", serviceRef.Name(), err)
	}

	defer r.enqueueDeleteTask(initJobDeleteTask{
		serviceID:   serviceCreate.ID,
		serviceName: serviceRef.Name(),
	})

	waitErr := r.waitJob(jobCtx, serviceCreate.ID, serviceRef.Name())
	finishedAt := time.Now().UTC()
	logs, truncated, logsErr := r.loadServiceLogs(ctx, serviceCreate.ID, serviceRef.Name())
	logsAvailable := logsErr == nil
	if logsErr != nil {
		slog.WarnContext(
			ctx,
			"[initjob] failed to fetch logs",
			slog.String("service", serviceRef.Name()),
			slog.Any("err", logsErr),
		)
	}
	execution := Result{
		ServiceName: spec.ServiceName, JobName: spec.Job.Name, Status: initJobExecutionStatus(waitErr),
		StartedAt: startedAt, FinishedAt: finishedAt, Logs: logs,
		LogsAvailable: logsAvailable, LogsTruncated: truncated,
	}
	if failed, ok := errors.AsType[*JobFailedError](waitErr); ok && logsAvailable {
		failed.logs = initJobDiagnosticLogLines(logs)
	}
	return execution, waitErr
}

func (r *InitJobRunner) waitJob(
	ctx context.Context,
	serviceID string,
	jobName string,
) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait init job %s: %w", jobName, ctx.Err())
		case <-ticker.C:
			tasks, err := r.dockerClient.TaskList(ctx, dockerswarm.TaskListOptions{
				Filters: filters.NewArgs(filters.Arg("service", serviceID)),
			})
			if err != nil {
				return fmt.Errorf("inspect init job %s status: %w", jobName, err)
			}
			if len(tasks) == 0 {
				continue
			}

			sort.Slice(tasks, func(i, j int) bool {
				return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
			})

			task := tasks[0]
			state := task.Status.State
			switch state {
			case dockerswarm.TaskStateNew,
				dockerswarm.TaskStateAllocated,
				dockerswarm.TaskStatePending,
				dockerswarm.TaskStateAssigned,
				dockerswarm.TaskStateAccepted,
				dockerswarm.TaskStatePreparing,
				dockerswarm.TaskStateReady,
				dockerswarm.TaskStateStarting,
				dockerswarm.TaskStateRunning:
				continue
			case dockerswarm.TaskStateComplete:
				return nil
			case dockerswarm.TaskStateFailed,
				dockerswarm.TaskStateRejected,
				dockerswarm.TaskStateShutdown,
				dockerswarm.TaskStateOrphaned,
				dockerswarm.TaskStateRemove:
				reason := strings.TrimSpace(task.Status.Err)
				if reason == "" {
					reason = strings.TrimSpace(task.Status.Message)
				}
				if reason == "" {
					reason = string(state)
				}

				return &JobFailedError{
					ID:     task.ID,
					Name:   jobName,
					Reason: reason,
				}
			}
		}
	}
}
