package deployer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/avast/retry-go/v5"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

const (
	initJobDeleteTasksBuffer    = 128
	initJobDeleteRetryAttempts  = uint(5)
	initJobDeleteRetryDelay     = 1 * time.Second
	initJobDeleteAttemptTimeout = 10 * time.Second
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
	metrics      InitJobMetrics

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
	metrics InitJobMetrics,
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

// Run executes a single init job and waits until completion.
func (r *InitJobRunner) Run(ctx context.Context, spec InitJobSpec) error {
	if spec.Job.Image == "" {
		return errors.New("init job image is required")
	}

	r.metrics.RecordInitJobRun(spec.StackName, spec.ServiceName)

	timeout := r.timeout
	if spec.Job.Timeout > 0 {
		timeout = spec.Job.Timeout
	}

	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	jobName := buildInitJobName(spec.StackName, spec.ServiceName, spec.Job.Name)
	serviceRef := swarm.NewServiceReference(spec.StackName, jobName)

	serviceSpec, err := r.buildInitServiceSpec(jobCtx, spec, serviceRef.Name())
	if err != nil {
		return err
	}

	serviceCreateOptions, err := r.buildInitServiceCreateOptions(spec.Job.Image)
	if err != nil {
		return fmt.Errorf("build init job service create options: %w", err)
	}

	serviceCreate, err := r.dockerClient.ServiceCreate(jobCtx, serviceSpec, serviceCreateOptions)
	if err != nil {
		return fmt.Errorf("create init job service %s: %w", serviceRef.Name(), err)
	}

	defer r.enqueueDeleteTask(initJobDeleteTask{
		serviceID:   serviceCreate.ID,
		serviceName: serviceRef.Name(),
	})

	return r.waitJob(jobCtx, serviceCreate.ID, serviceRef, serviceRef.Name())
}

func (r *InitJobRunner) waitJob(
	ctx context.Context,
	serviceID string,
	serviceRef swarm.ServiceReference,
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
					logs:   r.loadServiceLogs(ctx, serviceRef),
				}
			}
		}
	}
}

func (r *InitJobRunner) buildInitServiceSpec(
	ctx context.Context,
	spec InitJobSpec,
	serviceName string,
) (dockerswarm.ServiceSpec, error) {
	containerSpec := &dockerswarm.ContainerSpec{
		Image:   spec.Job.Image,
		Command: spec.Job.Command,
	}

	if len(spec.Job.Environment) > 0 {
		containerSpec.Env = make([]string, 0, len(spec.Job.Environment))
		for key, val := range spec.Job.Environment {
			containerSpec.Env = append(containerSpec.Env, fmt.Sprintf("%s=%s", key, val))
		}
	}

	networks := spec.Job.Networks
	if len(networks) == 0 {
		networks = spec.DefaultNetwork
	}
	networks = uniqueStrings(networks)

	networkAttachments := make([]dockerswarm.NetworkAttachmentConfig, 0, len(networks))
	for _, network := range networks {
		target := r.resolveNetworkTarget(ctx, spec.StackName, network)
		if target == "" {
			continue
		}
		networkAttachments = append(networkAttachments, dockerswarm.NetworkAttachmentConfig{Target: target})
	}

	secrets := mergeObjectRefs(spec.ServiceSecrets, spec.Job.Secrets)
	containerSpec.Secrets = make([]*dockerswarm.SecretReference, 0, len(secrets))
	for _, secret := range secrets {
		ref, err := r.swarmService.Secrets.ResolveReference(ctx, secret.Source, secret.Target)
		if err != nil {
			return dockerswarm.ServiceSpec{}, err
		}
		containerSpec.Secrets = append(containerSpec.Secrets, ref)
	}

	configs := mergeObjectRefs(spec.ServiceConfigs, spec.Job.Configs)
	containerSpec.Configs = make([]*dockerswarm.ConfigReference, 0, len(configs))
	for _, cfg := range configs {
		ref, err := r.swarmService.Configs.ResolveReference(ctx, cfg.Source, cfg.Target)
		if err != nil {
			return dockerswarm.ServiceSpec{}, err
		}
		containerSpec.Configs = append(containerSpec.Configs, ref)
	}

	replicas := uint64(1)

	return dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{
			Name: serviceName,
			Labels: map[string]string{
				"org.swarm-deploy.init-job.name":    serviceName,
				"org.swarm-deploy.init-job.stack":   spec.StackName,
				"org.swarm-deploy.init-job.service": spec.ServiceName,
			},
		},
		TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: containerSpec,
			Networks:      networkAttachments,
			RestartPolicy: &dockerswarm.RestartPolicy{
				Condition: dockerswarm.RestartPolicyConditionNone,
			},
		},
		Mode: dockerswarm.ServiceMode{
			Replicated: &dockerswarm.ReplicatedService{
				Replicas: &replicas,
			},
		},
	}, nil
}

func (r *InitJobRunner) resolveNetworkTarget(ctx context.Context, stackName, network string) string {
	candidates := []string{network}
	if !strings.HasPrefix(network, stackName+"_") {
		candidates = append(candidates, stackName+"_"+network)
	}
	if network == "default" {
		candidates = append(candidates, stackName+"_default")
	}

	for _, candidate := range uniqueStrings(candidates) {
		netResource, err := r.swarmService.Networks.Get(ctx, candidate)
		if err == nil {
			return netResource.ID
		}
		if !cerrdefs.IsNotFound(err) {
			// Fall through to try other candidates.
			continue
		}
	}

	return network
}

func (r *InitJobRunner) loadServiceLogs(ctx context.Context, serviceRef swarm.ServiceReference) []string {
	logs, err := r.swarmService.Services.Logs(ctx, serviceRef, swarm.ServiceLogsOptions{})
	if err != nil {
		slog.WarnContext(
			ctx,
			"[initjob] failed to fetch logs",
			slog.String("service", serviceRef.Name()),
			slog.Any("err", err),
		)

		return []string{}
	}
	return logs
}

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

func buildInitJobName(_, _, jobName string) string {
	return fmt.Sprintf("%s-%d", sanitizeForName(jobName), time.Now().UnixNano())
}

func sanitizeForName(v string) string {
	if v == "" {
		return "job"
	}

	var out strings.Builder
	for _, r := range strings.ToLower(v) {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}

	result := strings.Trim(out.String(), "-")
	if result == "" {
		return "job"
	}

	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))

	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}

		seen[v] = struct{}{}
		out = append(out, v)
	}

	return out
}

func mergeObjectRefs(a, b []compose.ObjectRef) []compose.ObjectRef {
	seen := map[string]struct{}{}
	out := make([]compose.ObjectRef, 0, len(a)+len(b))

	appendOne := func(ref compose.ObjectRef) {
		key := ref.Source + "|" + ref.Target
		if ref.Source == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}
		out = append(out, ref)
	}

	for _, ref := range a {
		appendOne(ref)
	}
	for _, ref := range b {
		appendOne(ref)
	}

	return out
}
