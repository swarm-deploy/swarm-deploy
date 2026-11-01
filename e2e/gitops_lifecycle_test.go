package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	gitdeployment "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	gitmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// One live stack survives six real Git revisions, including a failed rollout.
// No scheduler timing, Git transport, deployment, or Docker operations are mocked.
func TestRealSwarmGitOpsLifecycle(t *testing.T) {
	base := newExternalSwarmFixtureWithTimeout(t, 4*time.Minute)
	remote, writer, checkout := filepath.Join(t.TempDir(), "remote.git"), t.TempDir(), t.TempDir()
	gitCommand(t, base.ctx, writer, "init", "--bare", "--initial-branch=main", remote)
	gitCommand(t, base.ctx, writer, "init", "--initial-branch=main")
	gitCommand(t, base.ctx, writer, "remote", "add", "origin", remote)
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(writer, name), []byte(content), 0o600))
	}
	write("stacks.yaml", fmt.Sprintf("stacks:\n  - name: %s\n    composeFile: compose.yaml\n", base.stack))
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
	runner := syncrun.New(configs, repository, metricGroup, f.bus, f.state, networks, f.reconciler, puller)

	apiCommand, mode, secretValue, configValue := "initial", "v1", "secret-v1", "config-v1"
	scheduler, broken := false, false
	manifest := func() string {
		command := fmt.Sprintf(`printf '%s:%%s:%%s:%%s\n' "$$MODE" "$$(cat /run/secrets/password)" "$$(cat /etc/app.conf)"; sleep 600`, apiCommand)
		health := ""
		if broken {
			command = "sleep 1; exit 23"
			// A task can never briefly report Running before exit 23; otherwise a
			// fast verifier could incorrectly accept the failing generation.
			health = "    healthcheck: {test: [CMD, 'false'], interval: 1s, timeout: 1s, retries: 30}\n"
		}
		api := lifecycleService("api", command, "")
		api = strings.Replace(api, "    deploy:\n", fmt.Sprintf("    environment: {MODE: %s}\n    secrets: [password]\n    configs: [{source: settings, target: /etc/app.conf}]\n%s    deploy:\n", mode, health), 1)
		services := api + lifecycleService("worker", "sleep 600", "")
		if scheduler {
			services += lifecycleService("scheduler", "sleep 600", "")
		}
		return fmt.Sprintf("services:\n%s\nsecrets:\n  password: {file: ./password.txt}\nconfigs:\n  settings: {file: ./settings.txt}\nnetworks:\n  default:\n    ipam:\n      config: [{subnet: %s}]\n", services, f.subnet)
	}
	state := func(t *testing.T) gitmodel.Runtime {
		t.Helper()
		s, err := f.state.Read(f.ctx)
		require.NoError(t, err)
		return s
	}
	history := func(t *testing.T) []model.DeploymentSummary {
		t.Helper()
		page, err := f.store.List(f.ctx, model.ListFilter{Stacks: []string{f.stack}})
		require.NoError(t, err)
		return page.Deployments
	}
	publications := func(t *testing.T) []outbox.Publication {
		t.Helper()
		p, err := f.outbox.ListPublications(f.ctx, outbox.ListFilter{})
		require.NoError(t, err)
		return p
	}
	drain := func(t *testing.T) {
		t.Helper()
		for range 100 {
			worked, err := f.bus.ProcessNext(f.ctx)
			require.NoError(t, err)
			if !worked {
				require.Empty(t, publications(t))
				return
			}
		}
		t.Fatal("outbox did not drain within 100 deliveries")
	}
	var previousCompose, sha, failedID string
	var failedDeployment model.Deployment
	var previousAPI dockerswarm.Service
	var previousTasks []string
	var worker, scheduled gitopsServiceSnapshot
	var originalSecret, originalConfig, latestSecret, latestConfig string
	var seenSHAs []string

	for revision := range 6 {
		if !t.Run(fmt.Sprintf("revision_%d", revision), func(t *testing.T) {
			switch revision {
			case 1:
				apiCommand, mode = "updated", "v2"
			case 2:
				scheduler = true
			case 3:
				secretValue, configValue = "secret-v2", "config-v2"
			case 4:
				broken = true
			case 5:
				broken, scheduler, apiCommand = false, false, "recovered"
			}
			serviceCount := 2
			if scheduler {
				serviceCount++
			}
			content := manifest()
			if revision == 3 {
				require.Equal(t, previousCompose, content, "rotation changes resource files only")
			}
			write("compose.yaml", content)
			write("password.txt", secretValue+"\n")
			write("settings.txt", configValue+"\n")
			gitCommand(t, f.ctx, writer, "add", ".")
			gitCommand(t, f.ctx, writer, "-c", "user.name=E2E", "-c", "user.email=e2e@example.invalid", "commit", "-m", fmt.Sprintf("revision %d", revision))
			sha = gitCommand(t, f.ctx, writer, "rev-parse", "HEAD")
			require.NotContains(t, seenSHAs, sha)
			seenSHAs = append(seenSHAs, sha)
			gitCommand(t, f.ctx, writer, "push", "origin", "main")
			t.Logf("revision %d, commit %s", revision, sha)
			if revision == 0 {
				runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerStartup})
			} else {
				runner.Poll(f.ctx)
			}
			head, err := repository.Head(f.ctx)
			require.NoError(t, err)
			require.Equal(t, sha, head)
			checkedOut, err := repository.ReadFile(f.ctx, "compose.yaml")
			require.NoError(t, err)
			require.Equal(t, content, string(checkedOut))
			checkedSecret, err := repository.ReadFile(f.ctx, "password.txt")
			require.NoError(t, err)
			require.Equal(t, secretValue+"\n", string(checkedSecret))
			checkedConfig, err := repository.ReadFile(f.ctx, "settings.txt")
			require.NoError(t, err)
			require.Equal(t, configValue+"\n", string(checkedConfig))
			require.Len(t, history(t), revision+1, "each of these revisions changes effective desired state")
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
			runtime := state(t)
			require.Equal(t, sha, runtime.GitRevision)
			if revision > 0 {
				require.Equal(t, "updated", runtime.LastPollResult)
			}
			require.Equal(t, sha, runtime.Stacks[f.stack].LastCommit)
			if revision == 4 {
				require.Equal(t, "partial_error", runtime.LastSyncResult)
				require.NotEmpty(t, runtime.LastSyncError)
				require.Equal(t, model.Failed, latest.Status)
				require.False(t, latest.Retryable)
				require.Equal(t, "convergence_failed", latest.ErrorCode)
				require.Equal(t, model.PhaseVerification, latest.Phase)
				require.Equal(t, model.StageFailed, latest.VerificationStatus)
				require.Equal(t, model.StageSkipped, latest.CleanupStatus)
				require.True(t, latest.Verification.Known)
				require.Equal(t, []string{"api"}, latest.Verification.UnresolvedServices)
				require.Equal(t, latest.ErrorDiagnostic, latest.Attempts[0].ErrorDiagnostic)
				failedID, failedDeployment = latest.ID, latest
				failedService := f.settled(t, "api")
				require.Equal(t, previousAPI.ID, failedService.ID)
				require.Contains(t, failedService.Spec.TaskTemplate.ContainerSpec.Args, "sleep 1; exit 23")
				require.NotNil(t, failedService.UpdateStatus)
				require.Equal(t, dockerswarm.UpdateStatePaused, failedService.UpdateStatus.State)
				require.NotNil(t, failedService.UpdateStatus.StartedAt)
				var failedTask string
				f.poll(t, "revision 4 failing generation executed exit 23", func() bool {
					for _, task := range f.tasks(t, "api") {
						if !task.CreatedAt.Before(*failedService.UpdateStatus.StartedAt) && task.CurrentState == swarm.TaskStateFailed && task.ExitCode != nil && *task.ExitCode == 23 {
							failedTask = task.ID
							return true
						}
					}
					return false
				})
				require.NotContains(t, previousTasks, failedTask)
				require.Contains(t, latest.ErrorDiagnostic, failedTask)
				require.Contains(t, latest.ErrorDiagnostic, "exit_code=23")
				require.Contains(t, latest.ErrorDiagnostic, "state=failed")
				require.Contains(t, latest.ErrorDiagnostic, "service api failed to converge")
				require.Len(t, f.publications(t, events.TypeNameDeployFailed), 1)
				require.Empty(t, f.publications(t, events.TypeNameDeploySuccess))
			} else {
				assertSuccessfulHistory(t, latest)
				require.Equal(t, "success", runtime.LastSyncResult)
				require.Empty(t, runtime.LastSyncError)
				require.Equal(t, model.StageSkipped, latest.VerificationStatus)
				require.Equal(t, model.StageSucceeded, latest.CleanupStatus)
				require.Len(t, f.publications(t, events.TypeNameDeploySuccess), 1)
				require.Empty(t, f.publications(t, events.TypeNameDeployFailed))
				ids := f.runningIDs(t, "api", 1)
				current := f.settled(t, "api")
				require.Contains(t, current.Spec.TaskTemplate.ContainerSpec.Env, "MODE="+mode)
				require.Contains(t, current.Spec.TaskTemplate.ContainerSpec.Args[2], apiCommand)
				require.EqualValues(t, 1, *current.Spec.Mode.Replicated.Replicas)
				if revision > 0 {
					require.Equal(t, previousAPI.ID, current.ID)
					if revision == 2 {
						require.Equal(t, previousAPI.Spec, current.Spec)
						require.Equal(t, previousAPI.Version, current.Version)
						require.Equal(t, previousTasks, ids)
					} else {
						require.NotEqual(t, previousTasks, ids, "new specification must execute a new task generation")
					}
				}
				assertGitOpsTaskOutput(t, f, ids[0], fmt.Sprintf("%s:%s:%s:%s", apiCommand, mode, secretValue, configValue))
				previousAPI, previousTasks = current, ids
			}
			api := f.inspect(t, "api")
			require.Len(t, api.Spec.TaskTemplate.ContainerSpec.Secrets, 1)
			require.Len(t, api.Spec.TaskTemplate.ContainerSpec.Configs, 1)
			secretID, configID := api.Spec.TaskTemplate.ContainerSpec.Secrets[0].SecretID, api.Spec.TaskTemplate.ContainerSpec.Configs[0].ConfigID
			secrets, err := f.sw.Secrets.List(f.ctx, swarm.ListSecretsFilter{StackName: f.stack})
			require.NoError(t, err)
			configs, err := f.sw.Configs.List(f.ctx, swarm.ListConfigsFilter{StackName: f.stack})
			require.NoError(t, err)
			require.True(t, slices.ContainsFunc(secrets, func(item swarm.Secret) bool { return item.ID == secretID }), "mounted secret %s must exist", secretID)
			require.True(t, slices.ContainsFunc(configs, func(item swarm.Config) bool { return item.ID == configID }), "mounted config %s must exist", configID)
			if revision == 0 {
				originalSecret, originalConfig = secretID, configID
			} else if revision != 3 {
				require.Equal(t, latestSecret, secretID)
				require.Equal(t, latestConfig, configID)
			}
			latestSecret, latestConfig = secretID, configID
			resourceCount := 1
			if revision >= 3 {
				resourceCount = 2
			}
			require.Len(t, secrets, resourceCount)
			require.Len(t, configs, resourceCount)
			if revision < 3 {
				require.Equal(t, originalSecret, secretID)
				require.Equal(t, originalConfig, configID)
			} else {
				require.NotEqual(t, originalSecret, secretID)
				require.NotEqual(t, originalConfig, configID)
			}
			if revision > 0 {
				assertGitOpsServiceUnchanged(t, f, worker)
			}
			f.runningIDs(t, "worker", 1)
			worker = captureGitOpsService(t, f, "worker")
			require.EqualValues(t, 1, *worker.service.Spec.Mode.Replicated.Replicas)
			if revision >= 2 && revision < 5 {
				if revision > 2 {
					assertGitOpsServiceUnchanged(t, f, scheduled)
				}
				f.runningIDs(t, "scheduler", 1)
				scheduled = captureGitOpsService(t, f, "scheduler")
				require.EqualValues(t, 1, *scheduled.service.Spec.Mode.Replicated.Replicas)
			}
			if revision == 5 {
				_, err := f.sw.Services.Inspect(f.ctx, scheduled.service.ID)
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
				prior, err := f.store.Get(f.ctx, failedID)
				require.NoError(t, err)
				// History projects an older failure as Superseded when newer
				// desired state exists. Its immutable failed Attempt and all
				// failure diagnostics must survive recovery unchanged.
				require.Equal(t, model.Superseded, prior.Status)
				require.Equal(t, seenSHAs[4], prior.Commit)
				require.Equal(t, failedDeployment.LatestAttemptID, prior.LatestAttemptID)
				require.Equal(t, 1, prior.AttemptCount)
				require.Equal(t, failedDeployment.Attempts, prior.Attempts)
				require.Equal(t, model.Failed, prior.Attempts[0].Status)
				require.Equal(t, failedDeployment.ErrorCode, prior.ErrorCode)
				require.Equal(t, failedDeployment.ErrorDiagnostic, prior.ErrorDiagnostic)
				require.Equal(t, failedDeployment.Phase, prior.Phase)
				require.Equal(t, failedDeployment.VerificationStatus, prior.VerificationStatus)
				require.Equal(t, failedDeployment.Verification, prior.Verification)
			}
			eventType := events.TypeNameDeploySuccess
			if broken {
				eventType = events.TypeNameDeployFailed
			}
			completion := f.publications(t, eventType)[0].Envelope.Event.Details()
			require.Equal(t, sha, completion[events.AttributeCommit])
			require.Equal(t, latest.ID, completion[events.AttributeDeploymentID])
			require.Equal(t, latest.LatestAttemptID, completion[events.AttributeAttemptID])
			drain(t)
			runtime = state(t)
			require.Equal(t, sha, runtime.Stacks[f.stack].LastCommit)
			if !broken {
				require.Empty(t, runtime.Stacks[f.stack].LastError)
				require.Zero(t, runtime.Stacks[f.stack].Status.OutOfSynced)
				require.Equal(t, serviceCount, runtime.Stacks[f.stack].Status.Synced)
			} else {
				require.NotEmpty(t, runtime.Stacks[f.stack].LastError)
			}
			require.Len(t, runtime.Stacks[f.stack].Services, serviceCount)
			require.NotEmpty(t, runtime.Stacks[f.stack].SourceDigest)

			// Git polling without new commits must not run synchronization at all.
			// Interval reconciliation does run; effective-state deduplication must avoid
			// both a new persisted attempt and a Docker update, even for terminal failure.
			snapshots := []gitopsServiceSnapshot{captureGitOpsService(t, f, "api"), worker}
			if scheduler {
				snapshots = append(snapshots, scheduled)
			}
			beforeState, beforeHistory := state(t), history(t)
			runner.Poll(f.ctx)
			afterPoll := state(t)
			require.Equal(t, "no_change", afterPoll.LastPollResult)
			require.Equal(t, beforeState.LastSyncAt, afterPoll.LastSyncAt)
			require.Equal(t, beforeHistory, history(t))
			require.Empty(t, publications(t))
			runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerInterval})
			afterInterval := state(t)
			require.Equal(t, string(syncrun.TriggerInterval), afterInterval.LastSyncReason)
			require.Equal(t, afterPoll.LastPollAt, afterInterval.LastPollAt)
			require.True(t, afterInterval.LastSyncAt.After(beforeState.LastSyncAt))
			require.Equal(t, sha, afterInterval.GitRevision)
			loaded, err := compose.NewFileLoader().Load(f.ctx, f.path)
			require.NoError(t, err)
			require.Equal(t, loaded.Digest, afterInterval.Stacks[f.stack].SourceDigest)
			require.Equal(t, beforeHistory, history(t), "no new deployment or attempt for unchanged effective desired state")
			if broken {
				// Failure marks services OutOfSync without detecting drift.
				// Re-observing healthy worker/scheduler replicas must not invent
				// drift recovery events for that deployment-only failure state.
				require.Empty(t, f.publications(t, events.TypeNameServiceDriftFixed))
				require.Empty(t, f.publications(t, events.TypeNameDeploySuccess))
				require.Empty(t, f.publications(t, events.TypeNameDeployFailed))
				drain(t)
				runner.Run(f.ctx, syncrun.Task{Reason: syncrun.TriggerInterval})
				require.Equal(t, beforeHistory, history(t))
			}
			require.Empty(t, publications(t), "unchanged reconciliation must not duplicate domain events")
			for _, snapshot := range snapshots {
				assertGitOpsServiceUnchanged(t, f, snapshot)
			}
			if broken {
				require.Equal(t, failedID, f.latest(t).ID)
				require.Equal(t, model.Failed, f.latest(t).Status)
				require.NotEmpty(t, afterInterval.Stacks[f.stack].LastError)
				require.Equal(t, gitmodel.SyncStatus(gitmodel.SyncStatusOutOfSync), afterInterval.Stacks[f.stack].Services["api"].SyncStatus)
			}
			previousCompose = content
		}) {
			t.FailNow()
		}
	}
	require.Len(t, seenSHAs, 6)
	success, err := f.history.ReadRecent(f.ctx, eventhistory.QueryOptions{Types: []events.TypeName{events.TypeNameDeploySuccess}, Limit: 100})
	require.NoError(t, err)
	require.Len(t, success, 5)
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

type gitopsServiceSnapshot struct {
	service        dockerswarm.Service
	running, known []string
}

func captureGitOpsService(t *testing.T, f *lifecycleFixture, name string) gitopsServiceSnapshot {
	t.Helper()
	snapshot := gitopsServiceSnapshot{service: f.settled(t, name)}
	tasks := f.tasks(t, name)
	for _, task := range tasks {
		if task.DesiredState == swarm.TaskStateRunning && task.CurrentState == swarm.TaskStateRunning {
			snapshot.running = append(snapshot.running, task.ID)
		}
	}
	slices.Sort(snapshot.running)
	snapshot.known = taskIDs(tasks)
	return snapshot
}

func assertGitOpsServiceUnchanged(t *testing.T, f *lifecycleFixture, before gitopsServiceSnapshot) {
	t.Helper()
	name := strings.TrimPrefix(before.service.Spec.Name, f.stack+"_")
	after := captureGitOpsService(t, f, name)
	require.Equal(t, before.service.ID, after.service.ID, name)
	require.Equal(t, before.service.Spec, after.service.Spec, name)
	require.Equal(t, before.service.Version, after.service.Version, name)
	require.Equal(t, before.running, after.running, name)
	// Swarm may remove historical terminal tasks; new task identities are forbidden.
	require.Subset(t, before.known, after.known, name)
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
