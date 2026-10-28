package deployer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/avast/retry-go/v5"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
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

// Run executes a single init job, waits until completion and collects bounded Docker output.
func (r *InitJobRunner) Run(
	ctx context.Context,
	spec InitJobSpec,
) (InitJobResult, error) {
	if spec.Job.Image == "" {
		return InitJobResult{}, errors.New("init job image is required")
	}

	r.metrics.RecordInitJobRun(spec.StackName, spec.ServiceName)

	timeout := r.timeout
	if spec.Job.Timeout.Value > 0 {
		timeout = spec.Job.Timeout.Value
	}

	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	jobName := buildInitJobName(spec.StackName, spec.ServiceName, spec.Job.Name)
	serviceRef := swarm.NewServiceReference(spec.StackName, jobName)

	serviceSpec, err := r.buildInitServiceSpec(jobCtx, spec, serviceRef.Name())
	if err != nil {
		return InitJobResult{}, err
	}

	serviceCreateOptions, err := r.buildInitServiceCreateOptions(spec.Job.Image)
	if err != nil {
		return InitJobResult{}, fmt.Errorf("build init job service create options: %w", err)
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
		return InitJobResult{}, fmt.Errorf("create init job service %s: %w", serviceRef.Name(), err)
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
	execution := InitJobResult{
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

func (r *InitJobRunner) buildInitServiceSpec(
	ctx context.Context,
	spec InitJobSpec,
	serviceName string,
) (dockerswarm.ServiceSpec, error) {
	containerSpec := &dockerswarm.ContainerSpec{
		Image:   spec.Job.Image,
		Command: spec.Job.Entrypoint,
		Args:    spec.Job.Command,
	}

	if len(spec.Job.Environment.Map) > 0 {
		containerSpec.Env = make([]string, 0, len(spec.Job.Environment.Map))
		for key, val := range spec.Job.Environment.Map {
			containerSpec.Env = append(containerSpec.Env, fmt.Sprintf("%s=%s", key, val))
		}
	}

	networks := spec.Job.Networks.GetNames()
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
		ref := resolvedSecretReference(secret, spec.ResolvedSecrets)
		if ref == nil {
			var err error
			ref, err = r.swarmService.Secrets.ResolveReference(ctx, secret.Source, secret.Target)
			if err != nil {
				return dockerswarm.ServiceSpec{}, err
			}
		}
		containerSpec.Secrets = append(containerSpec.Secrets, ref)
	}

	configs := mergeObjectRefs(spec.ServiceConfigs, spec.Job.Configs)
	containerSpec.Configs = make([]*dockerswarm.ConfigReference, 0, len(configs))
	for _, cfg := range configs {
		ref := resolvedConfigReference(cfg, spec.ResolvedConfigs)
		if ref == nil {
			var err error
			ref, err = r.swarmService.Configs.ResolveReference(ctx, cfg.Source, cfg.Target)
			if err != nil {
				return dockerswarm.ServiceSpec{}, err
			}
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

func resolvedSecretReference(
	object compose.ObjectRef,
	resolved map[string]ResolvedResource,
) *dockerswarm.SecretReference {
	resource, ok := resolved[object.Source]
	if !ok {
		return nil
	}

	target := object.Target
	if target == "" {
		target = "/run/secrets/" + object.Source
	}
	uid, gid := objectOwner(object)

	return &dockerswarm.SecretReference{
		SecretID:   resource.ID,
		SecretName: resource.Name,
		File: &dockerswarm.SecretReferenceFileTarget{
			Name: target,
			UID:  uid,
			GID:  gid,
			Mode: objectMode(object.Mode),
		},
	}
}

func resolvedConfigReference(
	object compose.ObjectRef,
	resolved map[string]ResolvedResource,
) *dockerswarm.ConfigReference {
	resource, ok := resolved[object.Source]
	if !ok {
		return nil
	}

	ref := &dockerswarm.ConfigReference{
		ConfigID:   resource.ID,
		ConfigName: resource.Name,
	}
	if object.Target != "" {
		uid, gid := objectOwner(object)
		ref.File = &dockerswarm.ConfigReferenceFileTarget{
			Name: object.Target,
			UID:  uid,
			GID:  gid,
			Mode: objectMode(object.Mode),
		}
	}

	return ref
}

func objectMode(mode *os.FileMode) os.FileMode {
	if mode == nil {
		return initJobObjectFileMode
	}

	return *mode
}

func objectOwner(object compose.ObjectRef) (string, string) {
	uid := object.Uid
	if uid == "" {
		uid = "0"
	}
	gid := object.Gid
	if gid == "" {
		gid = "0"
	}

	return uid, gid
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

func (r *InitJobRunner) loadServiceLogs(
	ctx context.Context,
	serviceID string,
	serviceName string,
) ([]byte, bool, error) {
	logsCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initJobLogCollectionTimeout)
	defer cancel()
	reader, err := r.dockerClient.ServiceLogs(logsCtx, serviceID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "all",
	})
	if err != nil {
		return nil, false, fmt.Errorf("read logs for service %s: %w", serviceName, err)
	}
	defer reader.Close()

	tail := newTailBuffer(initJobLogLimit)
	if _, err = stdcopy.StdCopy(tail, tail, reader); err != nil {
		return nil, false, fmt.Errorf("read logs stream for service %s: %w", serviceName, err)
	}
	return tail.Bytes(), tail.Truncated(), nil
}

func initJobExecutionStatus(err error) InitJobStatus {
	if err == nil {
		return InitJobSucceeded
	}
	var failed *JobFailedError
	if errors.As(err, &failed) {
		return InitJobFailed
	}
	return InitJobInterrupted
}

func initJobDiagnosticLogLines(logs []byte) []string {
	text := strings.TrimSpace(string(logs))
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

type tailBuffer struct {
	limit int
	data  []byte
	total int64
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, data: make([]byte, 0, limit)}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.total += int64(written)
	if b.limit <= 0 || written == 0 {
		return written, nil
	}
	if written >= b.limit {
		b.data = append(b.data[:0], p[written-b.limit:]...)
		return written, nil
	}
	overflow := len(b.data) + written - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *tailBuffer) Bytes() []byte {
	if len(b.data) == 0 {
		return []byte{}
	}
	return append([]byte(nil), b.data...)
}

func (b *tailBuffer) Truncated() bool {
	return b.total > int64(b.limit)
}

var _ io.Writer = (*tailBuffer)(nil)

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
