package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	mobyswarm "github.com/moby/moby/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/dockertester"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventhistory "github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/gitpull"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackqueue"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	gitdeployment "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	gitmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"golang.org/x/sync/errgroup"
)

// One live stack survives seven real Git revisions, including a failed rollout.
// No scheduler timing, Git transport, deployment, or Docker operations are mocked.
func TestRealSwarmGitOpsLifecycle(t *testing.T) {
	f := newGitOpsLifecycleFixture(t)
	desired := gitOpsManifest{apiCommand: "initial", mode: "v1", secret: "secret-v1", config: "config-v1"}
	var api gitOpsAPIObservation
	var worker, scheduler dockertester.ServiceSnapshot
	var originalResources, rotatedResources gitOpsResourceRefs
	var composeBeforeRotation string
	var failed model.Deployment

	if !t.Run("initial_deployment", func(t *testing.T) {
		sha, content := f.pushRevision(t, "Initial deployment", desired)
		f.runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerStartup})
		latest := f.assertRevision(t, sha, content, desired)
		f.assertSuccessfulDeployment(t, latest)

		api = f.runningAPI(t, desired)
		originalResources = f.mountedResources(t, 1)
		worker = f.runningService(t, "worker", 2)
		f.assertCompletionDelivered(t, latest, events.TypeNameDeploySuccess, 2)
		f.assertHealthyReconciliationIsIdle(t, sha, api.snapshot, worker)
	}) {
		t.FailNow()
	}

	if !t.Run("repair_worker_replica_drift_without_git_commit", func(t *testing.T) {
		baseline, beforeHistory := f.latest(t), f.deploymentHistory(t)
		three := uint64(3)
		f.poll(t, "external worker scale accepted", func() bool {
			current := f.inspect(t, "worker")
			current.Spec.Mode.Replicated.Replicas = &three
			err := f.sw.Services.Update(f.ctx, current.ID, current.Version, current.Spec, dockerswarm.ServiceUpdateOptions{})
			if errors.Is(err, swarm.ErrServiceConflict) {
				return false
			}
			require.NoError(t, err)
			return true
		})
		f.runningIDs(t, "worker", 3)
		external := f.settled(t, "worker")
		f.runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerInterval})
		healed := f.runningService(t, "worker", 2)
		require.Equal(t, worker.Service.ID, healed.Service.ID)
		require.Equal(t, worker.Service.Spec, healed.Service.Spec)
		require.Greater(t, healed.Service.Version.Index, external.Service.Version.Index)
		require.Equal(t, string(gitmodel.SyncStatusSynced), f.syncStatus(t, "worker"))
		detected := f.publications(t, events.TypeNameServiceDrifted)
		require.Len(t, detected, 1)
		event, ok := detected[0].Envelope.Event.(*events.ServiceDrifted)
		require.True(t, ok)
		require.True(t, event.Resolved, "detected and repaired in the same reconciliation")
		require.Equal(t, "worker", event.ServiceName)
		require.Contains(t, event.Reason, "desired=2 actual=3")
		fixed := f.publications(t, events.TypeNameServiceDriftFixed)
		require.Len(t, fixed, 1)
		require.Less(t, detected[0].Envelope.PublicationSequence, fixed[0].Envelope.PublicationSequence)
		require.Equal(t, "worker", fixed[0].Envelope.Event.Details()[events.AttributeServiceName])
		require.Equal(t, beforeHistory, f.deploymentHistory(t), "self-heal creates no stack Deployment or Attempt")
		require.Equal(t, baseline, f.latest(t))
		f.delivered(t, events.TypeNameServiceDrifted, 1)
		f.delivered(t, events.TypeNameServiceDriftFixed, 1)
		worker = healed
		for range 3 {
			f.assertNoOpInterval(t, baseline.Commit, beforeHistory)
		}
		f.assertHealthyReconciliationIsIdle(t, baseline.Commit, api.snapshot, worker)
		f.delivered(t, events.TypeNameServiceDrifted, 1)
		f.delivered(t, events.TypeNameServiceDriftFixed, 1)
	}) {
		t.FailNow()
	}

	if !t.Run("update_api_command_and_environment", func(t *testing.T) {
		desired.apiCommand, desired.mode = "updated", "v2"
		sha, content := f.pushRevision(t, "Update API command and environment", desired)
		f.pollGit(t)
		latest := f.assertRevision(t, sha, content, desired)
		f.assertSuccessfulDeployment(t, latest)

		api = f.updatedAPI(t, api, desired)
		require.Equal(t, originalResources, f.mountedResources(t, 1))
		f.assertCompletionDelivered(t, latest, events.TypeNameDeploySuccess, 2)
		f.assertHealthyReconciliationIsIdle(t, sha, api.snapshot, worker)
	}) {
		t.FailNow()
	}

	if !t.Run("add_scheduler_service", func(t *testing.T) {
		desired.scheduler = true
		sha, content := f.pushRevision(t, "Add scheduler service", desired)
		f.pollGit(t)
		latest := f.assertRevision(t, sha, content, desired)
		f.assertSuccessfulDeployment(t, latest)

		// Retain the API baseline: the bounded idle check also proves isolation
		// across adding scheduler, including retained task generations.
		require.Equal(t, originalResources, f.mountedResources(t, 1))
		scheduler = f.runningService(t, "scheduler", 1)
		f.assertCompletionDelivered(t, latest, events.TypeNameDeploySuccess, 3)
		f.assertHealthyReconciliationIsIdle(t, sha, api.snapshot, worker, scheduler)
		composeBeforeRotation = content
	}) {
		t.FailNow()
	}

	if !t.Run("rotate_secret_and_config", func(t *testing.T) {
		desired.secret, desired.config = "secret-v2", "config-v2"
		sha, content := f.pushRevision(t, "Rotate secret and config", desired)
		require.Equal(t, composeBeforeRotation, content, "rotation changes resource files only")
		f.pollGit(t)
		latest := f.assertRevision(t, sha, content, desired)
		f.assertSuccessfulDeployment(t, latest)

		api = f.updatedAPI(t, api, desired)
		rotatedResources = f.mountedResources(t, 2)
		require.NotEqual(t, originalResources.secret, rotatedResources.secret)
		require.NotEqual(t, originalResources.config, rotatedResources.config)
		f.assertCompletionDelivered(t, latest, events.TypeNameDeploySuccess, 3)
		f.assertHealthyReconciliationIsIdle(t, sha, api.snapshot, worker, scheduler)
	}) {
		t.FailNow()
	}

	if !t.Run("failed_api_rollout", func(t *testing.T) {
		desired.broken = true
		sha, content := f.pushRevision(t, "Fail API rollout", desired)
		f.pollGit(t)
		failed = f.assertRevision(t, sha, content, desired)
		f.assertPausedAPIRollout(t, failed, api)

		require.Equal(t, rotatedResources, f.mountedResources(t, 2))
		require.NotEqual(t, originalResources.secret, rotatedResources.secret)
		require.NotEqual(t, originalResources.config, rotatedResources.config)
		f.assertCompletionDelivered(t, failed, events.TypeNameDeployFailed, 3)
		f.assertFailedReconciliationIsIdle(t, failed, worker, scheduler)
	}) {
		t.FailNow()
	}

	if !t.Run("recover_api", func(t *testing.T) {
		desired.broken, desired.apiCommand = false, "recovered"
		sha, content := f.pushRevision(t, "Recover API", desired)
		f.pollGit(t)
		latest := f.assertRevision(t, sha, content, desired)
		f.assertSuccessfulDeployment(t, latest)

		// Recovery keeps scheduler and compares API against the last healthy generation.
		api = f.updatedAPI(t, api, desired)
		require.Equal(t, rotatedResources, f.mountedResources(t, 2))
		require.NotEqual(t, originalResources.secret, rotatedResources.secret)
		require.NotEqual(t, originalResources.config, rotatedResources.config)
		f.assertFailedDeploymentSuperseded(t, failed)
		f.assertCompletionDelivered(t, latest, events.TypeNameDeploySuccess, 3)
		f.assertHealthyReconciliationIsIdle(t, sha, api.snapshot, worker, scheduler)
	}) {
		t.FailNow()
	}

	if !t.Run("prune_scheduler_without_other_service_changes", func(t *testing.T) {
		protected := []dockerswarm.Service{
			f.protectedService(t, f.inspect(t, "worker"), "unmanaged", f.stack, false, true),
			f.protectedService(t, f.inspect(t, "worker"), "optout", f.stack, true, false),
			f.protectedService(t, f.inspect(t, "worker"), "foreign", f.stack+"foreign", true, true),
		}
		beforeCompose := desired.compose(f.subnet)
		desired.scheduler = false
		sha, content := f.pushRevision(t, "Prune scheduler only", desired)
		require.Equal(t, strings.Replace(beforeCompose, lifecycleService("scheduler", "sleep 600", ""), "", 1), content,
			"pruning must not change any remaining service spec")
		f.pollGit(t)
		latest := f.assertRevision(t, sha, content, desired)
		f.assertSuccessfulDeployment(t, latest)

		require.Equal(t, rotatedResources, f.mountedResources(t, 2))
		f.assertSchedulerPruned(t, scheduler, sha)
		require.NotContains(t, f.runtime(t).Stacks[f.stack].Services, "scheduler")
		f.assertCompletionDelivered(t, latest, events.TypeNameDeploySuccess, 2)
		beforeHistory := f.deploymentHistory(t)
		for range 3 {
			f.assertNoOpInterval(t, sha, beforeHistory)
		}
		// Baselines predate pruning, proving it did not update API or worker tasks/specs.
		f.assertHealthyReconciliationIsIdle(t, sha, api.snapshot, worker)
		for _, service := range protected {
			current, err := f.sw.Services.Inspect(f.ctx, service.ID)
			require.NoError(t, err)
			require.Equal(t, service.Spec, current.Spec)
		}
		f.delivered(t, events.TypeNameServicePruned, 1)
	}) {
		t.FailNow()
	}

	require.Len(t, f.commits, 7)
	success, err := f.history.ReadRecent(f.ctx, eventhistory.QueryOptions{Types: []events.TypeName{events.TypeNameDeploySuccess}, Limit: 100})
	require.NoError(t, err)
	require.Len(t, success, 6)
}

// GitOps-specific wiring stays local; lifecycleFixture owns the real Swarm and stores.
type gitOpsLifecycleFixture struct {
	*lifecycleFixture
	writer     string
	repository gitx.Repository
	runner     waitingSyncRunner
	commits    []string
}

// waitingSyncRunner runs synchronization like the scheduler and then waits for queued stack operations,
// because sync runs only dispatch stacks to the stack queue.
type waitingSyncRunner struct {
	runner *syncrun.Runner
	stacks *stackqueue.Queue
}

func (w waitingSyncRunner) Run(ctx context.Context, task syncrun.Task) {
	w.runner.Run(ctx, task)
	_ = w.stacks.WaitIdle(ctx)
}

func (w waitingSyncRunner) Poll(ctx context.Context) {
	w.runner.Poll(ctx)
	_ = w.stacks.WaitIdle(ctx)
}

type gitOpsManifest struct {
	apiCommand, mode, secret, config string
	scheduler, broken                bool
}

type gitOpsAPIObservation struct {
	snapshot dockertester.ServiceSnapshot
	tasks    []string
}

type gitOpsResourceRefs struct {
	secret, config string
}

func newGitOpsLifecycleFixture(t *testing.T) *gitOpsLifecycleFixture {
	t.Helper()
	base := newExternalSwarmFixtureWithTimeout(t, 4*time.Minute)
	remote, writer, checkout := filepath.Join(t.TempDir(), "remote.git"), t.TempDir(), t.TempDir()
	gitCommand(t, base.ctx, writer, "init", "--bare", "--initial-branch=main", remote)
	gitCommand(t, base.ctx, writer, "init", "--initial-branch=main")
	gitCommand(t, base.ctx, writer, "remote", "add", "origin", remote)
	require.NoError(t, os.WriteFile(filepath.Join(writer, "stacks.yaml"), []byte(fmt.Sprintf("stacks:\n  - name: %s\n    composeFile: compose.yaml\n", base.stack)), 0o600))
	cfg := &config.Config{Spec: config.Spec{
		DataDir: t.TempDir(), Git: config.GitSpec{Repository: remote, Branch: "main"},
		StacksSource:   config.StacksSourceSpec{File: "stacks.yaml"},
		Sync:           config.SyncSpec{Policy: config.SyncPolicySpec{Prune: true, SelfHeal: true}},
		SecretRotation: config.SecretRotationSpec{Enabled: true, HashLength: 8},
	}}
	repository := gitx.NewRepository(cfg.Spec.Git, checkout)
	base.path = filepath.Join(checkout, "compose.yaml")
	f := newLifecycleComponents(t, base, cfg, repository)
	projection := gitdeployment.NewProjectionSubscriber(f.db, f.store, f.state)
	for _, typ := range []events.TypeName{events.TypeNameDeploySuccess, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted} {
		require.NoError(t, f.bus.Subscribe(typ, "gitops-runtime", projection))
	}
	configs := configstore.New(cfg, f.bus)
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "e2e"})
	puller := gitpull.NewPuller(repository, gitpull.NewMonitor(f.db, f.state, f.bus), metricGroup.Git, cfg.Spec.Git)
	networks := networkloop.NewCoordinator(f.db, configs, f.state, networkloop.New(f.sw.Networks), f.bus)
	stacks := stackqueue.New(f.reconciler, metricGroup.Sync, 1)
	stacks.Start(context.WithoutCancel(base.ctx))
	t.Cleanup(stacks.Stop)
	runner := syncrun.New(configs, repository, metricGroup, f.bus, f.state, networks, puller, stacks)
	return &gitOpsLifecycleFixture{
		lifecycleFixture: f, writer: writer, repository: repository,
		runner: waitingSyncRunner{runner: runner, stacks: stacks},
	}
}

func (m gitOpsManifest) compose(subnet string) string {
	command := fmt.Sprintf(`printf '%s:%%s:%%s:%%s\n' "$MODE" "$(cat /run/secrets/password)" "$(cat /etc/app.conf)"; sleep 600`, m.apiCommand)
	health := ""
	if m.broken {
		command = "sleep 1; exit 23"
		// A task can never briefly report Running before exit 23; otherwise a
		// fast verifier could incorrectly accept the failing generation.
		health = "    healthcheck: {test: [CMD, 'false'], interval: 1s, timeout: 1s, retries: 30}\n"
	}
	api := lifecycleService("api", command, "")
	api = strings.Replace(api, "    deploy:\n", fmt.Sprintf("    environment: {MODE: %s}\n    secrets: [password]\n    configs: [{source: settings, target: /etc/app.conf}]\n%s    deploy:\n", m.mode, health), 1)
	worker := strings.Replace(lifecycleService("worker", "sleep 600", ""), "replicas: 1", "replicas: 2", 1)
	services := api + worker
	if m.scheduler {
		services += lifecycleService("scheduler", "sleep 600", "")
	}
	return fmt.Sprintf("services:\n%s\nsecrets:\n  password: {file: ./password.txt}\nconfigs:\n  settings: {file: ./settings.txt}\nnetworks:\n  default:\n    ipam:\n      config: [{subnet: %s}]\n", services, subnet)
}

func (f *gitOpsLifecycleFixture) pushRevision(t *testing.T, message string, desired gitOpsManifest) (string, string) {
	t.Helper()
	content := desired.compose(f.subnet)
	for name, value := range map[string]string{"compose.yaml": content, "password.txt": desired.secret + "\n", "settings.txt": desired.config + "\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(f.writer, name), []byte(value), 0o600))
	}
	gitCommand(t, f.ctx, f.writer, "add", ".")
	gitCommand(t, f.ctx, f.writer, "-c", "user.name=E2E", "-c", "user.email=e2e@example.invalid", "commit", "-m", message)
	sha := gitCommand(t, f.ctx, f.writer, "rev-parse", "HEAD")
	require.NotContains(t, f.commits, sha)
	f.commits = append(f.commits, sha)
	gitCommand(t, f.ctx, f.writer, "push", "origin", "main")
	t.Logf("%s, commit %s", message, sha)
	return sha, content
}

func (f *gitOpsLifecycleFixture) pollGit(t *testing.T) {
	t.Helper()
	f.runner.Poll(f.ctx)
	require.Equal(t, "updated", f.runtime(t).LastPollResult)
}

func (f *gitOpsLifecycleFixture) assertRevision(t *testing.T, sha, content string, desired gitOpsManifest) model.Deployment {
	t.Helper()
	head, err := f.repository.Head(f.ctx)
	require.NoError(t, err)
	require.Equal(t, sha, head)
	for name, expected := range map[string]string{"compose.yaml": content, "password.txt": desired.secret + "\n", "settings.txt": desired.config + "\n"} {
		checkedOut, readErr := f.repository.ReadFile(f.ctx, name)
		require.NoError(t, readErr)
		require.Equal(t, expected, string(checkedOut), "checkout of %s", name)
	}
	require.Len(t, f.deploymentHistory(t), len(f.commits), "each of these revisions changes effective desired state")
	latest := f.latest(t)
	require.Equal(t, sha, latest.Commit)
	require.Equal(t, 1, latest.AttemptCount)
	require.Len(t, latest.Attempts, 1)
	require.NotNil(t, latest.Attempts[0].FinishedAt)
	require.Equal(t, model.StageSucceeded, latest.ApplyStatus)
	require.Equal(t, latest.Status, latest.Attempts[0].Status)
	require.Equal(t, latest.Phase, latest.Attempts[0].Phase)
	require.Equal(t, latest.ApplyStatus, latest.Attempts[0].ApplyStatus)
	require.Equal(t, latest.VerificationStatus, latest.Attempts[0].VerificationStatus)
	require.Equal(t, latest.CleanupStatus, latest.Attempts[0].CleanupStatus)
	require.Equal(t, latest.ErrorCode, latest.Attempts[0].ErrorCode)
	runtime := f.runtime(t)
	require.Equal(t, sha, runtime.GitRevision)
	require.Equal(t, sha, runtime.Stacks[f.stack].LastCommit)
	return latest
}

func (f *gitOpsLifecycleFixture) assertSuccessfulDeployment(t *testing.T, latest model.Deployment) {
	t.Helper()
	assertSuccessfulHistory(t, latest)
	runtime := f.runtime(t)
	require.Equal(t, "success", runtime.LastSyncResult)
	require.Empty(t, runtime.LastSyncError)
	require.Equal(t, model.StageSkipped, latest.VerificationStatus)
	require.Equal(t, model.StageSucceeded, latest.CleanupStatus)
	require.Len(t, f.publications(t, events.TypeNameDeploySuccess), 1)
	require.Empty(t, f.publications(t, events.TypeNameDeployFailed))
}

func (f *gitOpsLifecycleFixture) runningAPI(t *testing.T, desired gitOpsManifest) gitOpsAPIObservation {
	t.Helper()
	ids := f.runningIDs(t, "api", 1)
	current := f.settled(t, "api")
	require.Contains(t, current.Service.Spec.TaskTemplate.ContainerSpec.Env, "MODE="+desired.mode)
	require.Contains(t, current.Service.Spec.TaskTemplate.ContainerSpec.Args[2], desired.apiCommand)
	require.EqualValues(t, 1, *current.Service.Spec.Mode.Replicated.Replicas)
	assertGitOpsTaskOutput(t, f.lifecycleFixture, ids[0], fmt.Sprintf("%s:%s:%s:%s", desired.apiCommand, desired.mode, desired.secret, desired.config))
	return gitOpsAPIObservation{snapshot: current, tasks: ids}
}

func (f *gitOpsLifecycleFixture) updatedAPI(t *testing.T, previous gitOpsAPIObservation, desired gitOpsManifest) gitOpsAPIObservation {
	t.Helper()
	current := f.runningAPI(t, desired)
	require.Equal(t, previous.snapshot.Service.ID, current.snapshot.Service.ID)
	require.NotEqual(t, previous.tasks, current.tasks, "new specification must execute a new task generation")
	return current
}

func (f *gitOpsLifecycleFixture) runningService(t *testing.T, name string, replicas int) dockertester.ServiceSnapshot {
	t.Helper()
	f.runningIDs(t, name, replicas)
	current := f.settled(t, name)
	require.EqualValues(t, replicas, *current.Service.Spec.Mode.Replicated.Replicas)
	return current
}

func (f *gitOpsLifecycleFixture) mountedResources(t *testing.T, count int) gitOpsResourceRefs {
	t.Helper()
	api := f.inspect(t, "api")
	require.Len(t, api.Spec.TaskTemplate.ContainerSpec.Secrets, 1)
	require.Len(t, api.Spec.TaskTemplate.ContainerSpec.Configs, 1)
	refs := gitOpsResourceRefs{secret: api.Spec.TaskTemplate.ContainerSpec.Secrets[0].SecretID, config: api.Spec.TaskTemplate.ContainerSpec.Configs[0].ConfigID}
	secrets, err := f.sw.Secrets.List(f.ctx, swarm.ListSecretsFilter{StackName: f.stack})
	require.NoError(t, err)
	configs, err := f.sw.Configs.List(f.ctx, swarm.ListConfigsFilter{StackName: f.stack})
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(secrets, func(item swarm.Secret) bool { return item.ID == refs.secret }), "mounted secret %s must exist", refs.secret)
	require.True(t, slices.ContainsFunc(configs, func(item swarm.Config) bool { return item.ID == refs.config }), "mounted config %s must exist", refs.config)
	require.Len(t, secrets, count)
	require.Len(t, configs, count)
	return refs
}

func (f *gitOpsLifecycleFixture) assertPausedAPIRollout(t *testing.T, latest model.Deployment, previous gitOpsAPIObservation) {
	t.Helper()
	runtime := f.runtime(t)
	// Sync runs only dispatch stacks; the failed rollout is reported on the stack itself.
	require.Equal(t, "success", runtime.LastSyncResult)
	require.Empty(t, runtime.LastSyncError)
	require.Equal(t, "convergence_failed", runtime.Stacks[f.stack].LastError)
	require.Equal(t, model.Failed, latest.Status)
	require.False(t, latest.Retryable)
	require.Equal(t, "convergence_failed", latest.ErrorCode)
	require.Equal(t, model.PhaseVerification, latest.Phase)
	require.Equal(t, model.StageFailed, latest.VerificationStatus)
	require.Equal(t, model.StageSkipped, latest.CleanupStatus)
	require.True(t, latest.Verification.Known)
	require.Equal(t, []string{"api"}, latest.Verification.UnresolvedServices)
	require.Equal(t, latest.ErrorDiagnostic, latest.Attempts[0].ErrorDiagnostic)
	// A paused rollout is deliberately a failure baseline, not healthy stability.
	failedSnapshot, stableErr := f.observer.WaitStable(f.ctx, f.stack+"_api", time.Second)
	require.ErrorContains(t, stableErr, "rollout paused")
	failedService := failedSnapshot.Service
	require.Equal(t, previous.snapshot.Service.ID, failedService.ID)
	require.Contains(t, failedService.Spec.TaskTemplate.ContainerSpec.Args, "sleep 1; exit 23")
	require.NotNil(t, failedService.UpdateStatus)
	require.Equal(t, mobyswarm.UpdateStatePaused, failedService.UpdateStatus.State)
	require.NotNil(t, failedService.UpdateStatus.StartedAt)
	var failedTask string
	f.poll(t, "failing API generation executed exit 23", func() bool {
		for _, task := range f.tasks(t, "api") {
			if !task.CreatedAt.Before(*failedService.UpdateStatus.StartedAt) && task.CurrentState == swarm.TaskStateFailed && task.ExitCode != nil && *task.ExitCode == 23 {
				failedTask = task.ID
				return true
			}
		}
		return false
	})
	require.NotContains(t, previous.tasks, failedTask)
	require.Contains(t, latest.ErrorDiagnostic, failedTask)
	require.Contains(t, latest.ErrorDiagnostic, "exit_code=23")
	require.Contains(t, latest.ErrorDiagnostic, "state=failed")
	require.Contains(t, latest.ErrorDiagnostic, "service api failed to converge")
	require.Len(t, f.publications(t, events.TypeNameDeployFailed), 1)
	require.Empty(t, f.publications(t, events.TypeNameDeploySuccess))
}

func (f *gitOpsLifecycleFixture) assertSchedulerPruned(t *testing.T, scheduler dockertester.ServiceSnapshot, sha string) {
	t.Helper()
	_, err := f.sw.Services.Inspect(f.ctx, scheduler.Service.ID)
	require.ErrorIs(t, err, swarm.ErrServiceNotFound)
	pruned := f.publications(t, events.TypeNameServicePruned)
	require.Len(t, pruned, 1)
	event, ok := pruned[0].Envelope.Event.(*events.ServicePruned)
	require.True(t, ok)
	require.Equal(t, "scheduler", event.ServiceName)
	require.Equal(t, sha, event.Commit)
	removed := f.publications(t, events.TypeNameServiceRemovedFromDesired)
	require.Len(t, removed, 1)
	require.Equal(t, sha, removed[0].Envelope.Event.Details()[events.AttributeCommit])
}

func (f *gitOpsLifecycleFixture) assertFailedDeploymentSuperseded(t *testing.T, failed model.Deployment) {
	t.Helper()
	prior, err := f.store.Get(f.ctx, failed.ID)
	require.NoError(t, err)
	// History projects the older failure as Superseded; its immutable failed
	// Attempt and all failure diagnostics must survive recovery unchanged.
	require.Equal(t, model.Superseded, prior.Status)
	require.Equal(t, failed.Commit, prior.Commit)
	require.Equal(t, failed.LatestAttemptID, prior.LatestAttemptID)
	require.Equal(t, 1, prior.AttemptCount)
	require.Equal(t, failed.Attempts, prior.Attempts)
	require.Equal(t, model.Failed, prior.Attempts[0].Status)
	require.Equal(t, failed.ErrorCode, prior.ErrorCode)
	require.Equal(t, failed.ErrorDiagnostic, prior.ErrorDiagnostic)
	require.Equal(t, failed.Phase, prior.Phase)
	require.Equal(t, failed.VerificationStatus, prior.VerificationStatus)
	require.Equal(t, failed.Verification, prior.Verification)
}

func (f *gitOpsLifecycleFixture) assertCompletionDelivered(t *testing.T, latest model.Deployment, eventType events.TypeName, serviceCount int) {
	t.Helper()
	completion := f.publications(t, eventType)[0].Envelope.Event.Details()
	require.Equal(t, latest.Commit, completion[events.AttributeCommit])
	require.Equal(t, latest.ID, completion[events.AttributeDeploymentID])
	require.Equal(t, latest.LatestAttemptID, completion[events.AttributeAttemptID])
	f.drainPublications(t)
	runtime := f.runtime(t)
	require.Equal(t, latest.Commit, runtime.Stacks[f.stack].LastCommit)
	if eventType == events.TypeNameDeploySuccess {
		require.Empty(t, runtime.Stacks[f.stack].LastError)
		require.Zero(t, runtime.Stacks[f.stack].Status.OutOfSynced)
		require.Equal(t, serviceCount, runtime.Stacks[f.stack].Status.Synced)
	} else {
		require.NotEmpty(t, runtime.Stacks[f.stack].LastError)
	}
	require.Len(t, runtime.Stacks[f.stack].Services, serviceCount)
	require.NotEmpty(t, runtime.Stacks[f.stack].SourceDigest)
}

func (f *gitOpsLifecycleFixture) assertNoOpGitPoll(t *testing.T) []model.DeploymentSummary {
	t.Helper()
	beforeState, beforeHistory := f.runtime(t), f.deploymentHistory(t)
	f.runner.Poll(f.ctx)
	afterPoll := f.runtime(t)
	require.Equal(t, "no_change", afterPoll.LastPollResult)
	require.Equal(t, beforeState.LastSyncAt, afterPoll.LastSyncAt)
	require.Equal(t, beforeHistory, f.deploymentHistory(t))
	require.Empty(t, f.allPublications(t))
	return beforeHistory
}

func (f *gitOpsLifecycleFixture) assertNoOpInterval(t *testing.T, sha string, beforeHistory []model.DeploymentSummary) gitmodel.Runtime {
	t.Helper()
	afterPoll := f.runtime(t)
	f.runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerInterval})
	afterInterval := f.runtime(t)
	require.Equal(t, string(syncrun.TriggerInterval), afterInterval.LastSyncReason)
	require.Equal(t, afterPoll.LastPollAt, afterInterval.LastPollAt)
	require.True(t, afterInterval.LastSyncAt.After(afterPoll.LastSyncAt))
	require.Equal(t, sha, afterInterval.GitRevision)
	loaded, err := compose.NewFileLoader().Load(f.ctx, f.path)
	require.NoError(t, err)
	require.Equal(t, loaded.Digest, afterInterval.Stacks[f.stack].SourceDigest)
	require.Equal(t, beforeHistory, f.deploymentHistory(t), "no new deployment or attempt for unchanged effective desired state")
	return afterInterval
}

func (f *gitOpsLifecycleFixture) assertHealthyReconciliationIsIdle(t *testing.T, sha string, apiBaseline dockertester.ServiceSnapshot, unaffected ...dockertester.ServiceSnapshot) {
	t.Helper()
	beforeHistory := f.assertNoOpGitPoll(t)
	f.assertNoOpInterval(t, sha, beforeHistory)
	require.Empty(t, f.allPublications(t), "unchanged reconciliation must not duplicate domain events")
	f.assertServicesUnchanged(t, append([]dockertester.ServiceSnapshot{apiBaseline}, unaffected...)...)
}

func (f *gitOpsLifecycleFixture) assertFailedReconciliationIsIdle(t *testing.T, failed model.Deployment, unaffected ...dockertester.ServiceSnapshot) {
	t.Helper()
	// Exit status was verified before delivery. Capture the terminal paused state
	// explicitly rather than accepting it as a healthy WaitStable baseline.
	apiBaseline, err := f.observer.Snapshot(f.ctx, f.stack+"_api")
	require.NoError(t, err)
	require.NotNil(t, apiBaseline.Service.UpdateStatus)
	require.Equal(t, mobyswarm.UpdateStatePaused, apiBaseline.Service.UpdateStatus.State)
	beforeHistory := f.assertNoOpGitPoll(t)
	afterInterval := f.assertNoOpInterval(t, failed.Commit, beforeHistory)
	// A deployment-only OutOfSync state must not invent drift recovery events
	// when healthy worker/scheduler replicas are observed again.
	require.Empty(t, f.publications(t, events.TypeNameServiceDriftFixed))
	require.Empty(t, f.publications(t, events.TypeNameDeploySuccess))
	require.Empty(t, f.publications(t, events.TypeNameDeployFailed))
	f.drainPublications(t)
	f.runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerInterval})
	require.Equal(t, beforeHistory, f.deploymentHistory(t))
	require.Empty(t, f.allPublications(t), "unchanged reconciliation must not duplicate domain events")
	f.assertServicesUnchanged(t, append([]dockertester.ServiceSnapshot{apiBaseline}, unaffected...)...)
	require.Equal(t, failed.ID, f.latest(t).ID)
	require.Equal(t, model.Failed, f.latest(t).Status)
	require.NotEmpty(t, afterInterval.Stacks[f.stack].LastError)
	require.Equal(t, gitmodel.SyncStatus(gitmodel.SyncStatusOutOfSync), afterInterval.Stacks[f.stack].Services["api"].SyncStatus)
}

// Observe all services over the same full window. Baselines for unaffected
// services predate the transition, so updates and new tasks cannot become a fresh
// accepted baseline. Report errors on the test goroutine after every watch exits.
func (f *gitOpsLifecycleFixture) assertServicesUnchanged(t *testing.T, snapshots ...dockertester.ServiceSnapshot) {
	t.Helper()
	group, ctx := errgroup.WithContext(f.ctx)
	for _, snapshot := range snapshots {
		group.Go(func() error {
			return f.observer.WaitUnchanged(ctx, snapshot, 2*time.Second)
		})
	}
	require.NoError(t, group.Wait())
}

func (f *gitOpsLifecycleFixture) runtime(t *testing.T) gitmodel.Runtime {
	t.Helper()
	state, err := f.state.Read(f.ctx)
	require.NoError(t, err)
	return state
}

func (f *gitOpsLifecycleFixture) deploymentHistory(t *testing.T) []model.DeploymentSummary {
	t.Helper()
	page, err := f.store.List(f.ctx, model.ListFilter{Stacks: []string{f.stack}})
	require.NoError(t, err)
	return page.Deployments
}

func (f *gitOpsLifecycleFixture) allPublications(t *testing.T) []outbox.Publication {
	t.Helper()
	publications, err := f.outbox.ListPublications(f.ctx, outbox.ListFilter{})
	require.NoError(t, err)
	return publications
}

func (f *gitOpsLifecycleFixture) drainPublications(t *testing.T) {
	t.Helper()
	for range 100 {
		worked, err := f.bus.ProcessNext(f.ctx)
		require.NoError(t, err)
		if !worked {
			require.Empty(t, f.allPublications(t))
			return
		}
	}
	t.Fatal("outbox did not drain within 100 deliveries")
}

func gitCommand(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, output)
	return strings.TrimSpace(string(output))
}

func assertGitOpsTaskOutput(t *testing.T, f *lifecycleFixture, taskID, expected string) {
	t.Helper()
	var observed []string
	f.poll(t, "current api task "+taskID+" read mounted resource contents: "+expected, func() bool {
		entries, errs, err := f.sw.Services.TaskLogs(f.ctx, taskID, swarm.TaskLogsOptions{Limit: 10})
		require.NoError(t, err)
		observed = nil
		for entry := range entries {
			observed = append(observed, entry.Message)
		}
		for err := range errs {
			require.NoError(t, err)
		}
		return slices.Contains(observed, expected)
	})
	assert.Contains(t, observed, expected)
}
