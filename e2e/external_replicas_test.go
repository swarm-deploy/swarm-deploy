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
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Give each fixture a distinct subnet in the benchmark address range. Reusing
// Swarm auto-allocated pools before worker-side overlay teardown can reject tasks.
var externalSubnetSequence atomic.Uint32

// All mutations use internal/swarm managers backed by the real Engine. No test
// doubles or production hooks are used, including for external scheduler writes.
type externalSwarmFixture struct {
	ctx         context.Context
	sw          *swarm.Swarm
	deployer    deployer.StackDeployer
	stack, path string
	subnet      string
}

func newExternalSwarmFixture(t *testing.T, waitTimeout ...time.Duration) *externalSwarmFixture {
	t.Helper()
	if os.Getenv("SWARM_DEPLOY_E2E") != "1" {
		t.Skip("set SWARM_DEPLOY_E2E=1 to run against a real initialized Docker Swarm")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cli.Close()) })
	f := &externalSwarmFixture{ctx: ctx, sw: swarm.NewSwarm(cli),
		stack: fmt.Sprintf("sdexternal%d", time.Now().UnixNano()), path: filepath.Join(t.TempDir(), "compose.yaml"),
		subnet: fmt.Sprintf("198.18.%d.0/24", externalSubnetSequence.Add(1))}
	timeout := 40 * time.Second
	if len(waitTimeout) > 0 {
		timeout = waitTimeout[0]
	}
	f.deployer = deployer.NewDeployer(100*time.Millisecond, timeout, cli, f.sw, nil,
		deployer.NewSecretResolver(fs.NewLocalFileSystem(), deployer.PlaintextDecoder{}))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cleanupCancel()
		// Capture diagnostics before cleanup, including failures during partial apply.
		if t.Failed() {
			for _, args := range [][]string{
				{"service", "ls"}, {"stack", "ps", "--no-trunc", f.stack},
				{"service", "inspect", f.stack + "_job"}, {"service", "logs", f.stack + "_cron"},
			} {
				output, commandErr := exec.CommandContext(cleanupCtx, "docker", args...).CombinedOutput()
				t.Logf("docker %v: %v\n%s", args, commandErr, output)
			}
		}
		output, cleanupErr := exec.CommandContext(cleanupCtx, "docker", "stack", "rm", f.stack).CombinedOutput()
		assert.NoError(t, cleanupErr, "stack cleanup: %s", output)
		assert.Eventually(t, func() bool {
			remaining, listErr := f.sw.Services.ListServices(cleanupCtx, swarm.ListServicesFilter{Stack: f.stack})
			_, networkErr := f.sw.Networks.Get(cleanupCtx, f.stack+"_default")
			return listErr == nil && len(remaining) == 0 && errors.Is(networkErr, swarm.ErrNetworkNotFound)
		}, 20*time.Second, 100*time.Millisecond, "all stack resources must be removed")
	})
	return f
}

func (f *externalSwarmFixture) load(t *testing.T, replicas uint64, command, extra string) compose.Compose {
	t.Helper()
	manifest := fmt.Sprintf(`services:
  job:
    image: busybox:1.36
    command: ["sh", "-c", %q]
    deploy:
      mode: replicated
      replicas: %d
      restart_policy:
        condition: none
      labels:
        org.swarm-deploy.service.sync.policy.selfHeal: "false"
        org.swarm-deploy.service.sync.policy.replicasManaged: "false"
%s
networks:
  default:
    ipam:
      config:
        - subnet: %s
`, command, replicas, extra, f.subnet)
	require.NoError(t, os.WriteFile(f.path, []byte(manifest), 0o600))
	file, err := compose.NewFileLoader().Load(f.ctx, f.path)
	require.NoError(t, err)
	return file.Compose
}

func (f *externalSwarmFixture) deploy(t *testing.T, desired compose.Compose, options deployer.StackOptions) deployer.Result {
	t.Helper()
	result, err := f.deployer.DeployStack(f.ctx, deployer.StackRequest{
		StackName: f.stack, SourceComposePath: f.path, Desired: desired, Options: options,
	})
	require.NoError(t, err)
	require.True(t, result.Verification.Known)
	require.Empty(t, result.Verification.UnresolvedServices)
	f.accepted(t, desired)
	return result
}

func (f *externalSwarmFixture) inspect(t *testing.T, name string) dockerswarm.Service {
	t.Helper()
	service, err := f.sw.Services.Inspect(f.ctx, f.stack+"_"+name)
	require.NoError(t, err)
	return service
}

// Observe a settled rollout before version assertions: Swarm itself writes
// rollout metadata asynchronously after ServiceUpdate, independently of deploy.
func (f *externalSwarmFixture) settledJob(t *testing.T) dockerswarm.Service {
	t.Helper()
	var last dockerswarm.Service
	stableSince := time.Now()
	f.poll(t, "service version and rollout metadata settled", func() bool {
		current := f.inspect(t, "job")
		pending := current.UpdateStatus != nil && (current.UpdateStatus.State == dockerswarm.UpdateStateUpdating ||
			current.UpdateStatus.State == dockerswarm.UpdateStateRollbackStarted)
		if pending || current.Version != last.Version {
			stableSince = time.Now()
		}
		last = current
		return !pending && time.Since(stableSince) >= time.Second
	})
	return last
}

func (f *externalSwarmFixture) externalUpdate(t *testing.T, change func(*dockerswarm.ServiceSpec)) {
	t.Helper()
	f.poll(t, "external update accepted despite scheduler metadata conflicts", func() bool {
		current := f.inspect(t, "job")
		change(&current.Spec)
		err := f.sw.Services.Update(f.ctx, current.ID, current.Version, current.Spec, dockerswarm.ServiceUpdateOptions{})
		if errors.Is(err, swarm.ErrServiceConflict) {
			return false
		}
		require.NoError(t, err)
		return true
	})
}

func (f *externalSwarmFixture) tasks(t *testing.T, name string) []swarm.ServiceTask {
	t.Helper()
	tasks, err := f.sw.Services.ListTasks(f.ctx, f.stack+"_"+name)
	require.NoError(t, err)
	return tasks
}

func (f *externalSwarmFixture) accepted(t *testing.T, desired compose.Compose) {
	t.Helper()
	converted, err := converter.New(f.sw.Networks, f.sw.Configs, f.sw.Secrets).Convert(f.ctx, f.stack, desired)
	require.NoError(t, err)
	for _, expected := range converted.Services {
		observed, inspectErr := f.sw.Services.Inspect(f.ctx, expected.Name)
		require.NoError(t, inspectErr)
		require.NotEmpty(t, observed.ID)
		require.Equal(t, expected.Name, observed.Spec.Name)
		require.True(t, services.SpecsEqual(observed.Spec, expected), "managed specification must be accepted: %s", expected.Name)
	}
}

func (f *externalSwarmFixture) poll(t *testing.T, description string, predicate func() bool) {
	t.Helper()
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-f.ctx.Done():
			t.Fatalf("%s: %v", description, f.ctx.Err())
		case <-timer.C:
			t.Fatalf("timed out: %s", description)
		case <-ticker.C:
		}
	}
}

func taskIDs(tasks []swarm.ServiceTask) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	slices.Sort(ids)
	return ids
}

func (f *externalSwarmFixture) running(t *testing.T, count int) {
	t.Helper()
	f.poll(t, fmt.Sprintf("%d running tasks", count), func() bool {
		running := 0
		for _, task := range f.tasks(t, "job") {
			if task.CurrentState == swarm.TaskStateRunning && task.DesiredState == swarm.TaskStateRunning {
				running++
			}
		}
		return running == count
	})
}

// Observe a bounded interval for delayed task creation, rather than checking only
// the instant DeployStack returns. The baseline is captured after external
// rollout metadata settles, so even an otherwise redundant write is detected.
func (f *externalSwarmFixture) unchanged(t *testing.T, before dockerswarm.Service, ids []string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		after := f.inspect(t, "job")
		require.Equal(t, before.ID, after.ID)
		require.Equal(t, before.Spec, after.Spec)
		require.Equal(t, before.Version.Index, after.Version.Index, "unchanged deployment must not issue a service update")
		if before.UpdateStatus != nil && after.UpdateStatus != nil {
			require.Equal(t, before.UpdateStatus.StartedAt, after.UpdateStatus.StartedAt, "no new rollout")
		}
		observedIDs := taskIDs(f.tasks(t, "job"))
		if *before.Spec.Mode.Replicated.Replicas == 0 {
			// Docker may garbage-collect shutdown history while the service is
			// idle. New task identities are still forbidden.
			require.Subset(t, ids, observedIDs, "no unscheduled execution during idle update")
		} else {
			require.Equal(t, ids, observedIDs, "no additional execution or task replacement")
		}
		select {
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		case <-timer.C:
			return
		case <-ticker.C:
		}
	}
}

func TestExternalReplicasInitialCreationOnRealSwarm(t *testing.T) {
	for _, replicas := range []uint64{0, 1} {
		t.Run(fmt.Sprintf("replicas=%d", replicas), func(t *testing.T) {
			f := newExternalSwarmFixture(t)
			// An impossible constraint proves specification-only acceptance cannot
			// accidentally depend on fast container startup on the CI runner.
			desired := f.load(t, replicas, "sleep 300", "      placement:\n        constraints: [\"node.labels.sd_external_absent == "+f.stack+"\"]")
			result := f.deploy(t, desired, deployer.StackOptions{})
			require.Equal(t, []string{"job"}, result.SpecificationOnlyServices)
			service := f.inspect(t, "job")
			require.Nil(t, service.UpdateStatus, "initial creation has no rolling update")
			require.Equal(t, replicas, *service.Spec.Mode.Replicated.Replicas)
			for _, task := range f.tasks(t, "job") {
				require.NotEqual(t, swarm.TaskStateRunning, task.CurrentState)
			}
			f.deploy(t, desired, deployer.StackOptions{})
			require.Equal(t, service.Spec, f.inspect(t, "job").Spec)
		})
	}
}

func TestExternalReplicaChangesOnRealSwarm(t *testing.T) {
	f := newExternalSwarmFixture(t)
	desired := f.load(t, 0, "sleep 300", "")
	f.deploy(t, desired, deployer.StackOptions{})
	two := uint64(2)
	f.externalUpdate(t, func(spec *dockerswarm.ServiceSpec) { spec.Mode.Replicated.Replicas = &two })
	f.running(t, 2)
	external := f.settledJob(t)
	ids := taskIDs(f.tasks(t, "job"))
	for range 3 {
		f.deploy(t, desired, deployer.StackOptions{})
		f.unchanged(t, external, ids)
	}
}

func TestExternalForceUpdateAndRetryOnRealSwarm(t *testing.T) {
	f := newExternalSwarmFixture(t)
	desired := f.load(t, 0, "exit 23", "")
	f.deploy(t, desired, deployer.StackOptions{})
	for generation := range 2 {
		one := uint64(1)
		f.externalUpdate(t, func(spec *dockerswarm.ServiceSpec) {
			spec.Mode.Replicated.Replicas = &one
			spec.TaskTemplate.ForceUpdate++
		})
		f.poll(t, "external execution failed", func() bool {
			tasks := f.tasks(t, "job")
			if len(tasks) != generation+1 {
				return false
			}
			for _, task := range tasks {
				if task.CurrentState != swarm.TaskStateFailed {
					return false
				}
			}
			return true
		})
		external := f.settledJob(t)
		ids := taskIDs(f.tasks(t, "job"))
		// An accepted unchanged spec remains valid even when external execution
		// failed. Explicit deployment retry must not force another job run.
		for _, retry := range []bool{false, true} {
			f.deploy(t, desired, deployer.StackOptions{RetryFailedUpdates: retry})
			f.unchanged(t, external, ids)
		}
	}
}

func TestExternalActiveAndIdleUpdateOnRealSwarm(t *testing.T) {
	f := newExternalSwarmFixture(t)
	desired := f.load(t, 1, "sleep 300", "")
	f.deploy(t, desired, deployer.StackOptions{})
	f.running(t, 1)
	before := f.settledJob(t)
	ids := taskIDs(f.tasks(t, "job"))
	desired.Services[0].Environment = compose.Environment{Map: map[string]string{"CHANGED": "yes"}, Keys: []string{"CHANGED"}}
	// Sorted after job: this must still be applied after the blocked error.
	zero := uint64(0)
	desired.Services = append(desired.Services, compose.Service{Name: "web", Image: "busybox:1.36", Deploy: compose.ServiceDeploy{Replicas: &zero}})
	result, err := f.deployer.DeployStack(f.ctx, deployer.StackRequest{StackName: f.stack, SourceComposePath: f.path, Desired: desired})
	require.ErrorIs(t, err, services.ErrUpdateBlocked)
	require.Equal(t, "apply stack", deployer.FailedStep(err))
	require.False(t, result.Verification.Known)
	web := f.inspect(t, "web")
	require.NotEmpty(t, web.ID, "independent service is applied despite blocked job")
	f.unchanged(t, before, ids)
	f.running(t, 1)
	// Arrange an actual idle window externally, then retry the same deployment.
	f.externalUpdate(t, func(spec *dockerswarm.ServiceSpec) {
		spec.Mode.Replicated.Replicas = &zero
		spec.TaskTemplate.ForceUpdate = 42
	})
	f.poll(t, "no active tasks after external scale down", func() bool {
		for _, task := range f.tasks(t, "job") {
			switch task.CurrentState {
			case swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed, swarm.TaskStateRejected, swarm.TaskStateRemove, swarm.TaskStateOrphaned:
			default:
				return false
			}
		}
		return true
	})
	// Keep the independent service idle as well so it does not affect verification.
	desired.Services[1].Deploy.Replicas = &zero
	ids = taskIDs(f.tasks(t, "job"))
	result = f.deploy(t, desired, deployer.StackOptions{RetryFailedUpdates: true})
	require.Equal(t, []string{"job"}, result.SpecificationOnlyServices)
	accepted := f.settledJob(t)
	require.Zero(t, *accepted.Spec.Mode.Replicated.Replicas)
	require.Equal(t, uint64(42), accepted.Spec.TaskTemplate.ForceUpdate)
	require.Contains(t, accepted.Spec.TaskTemplate.ContainerSpec.Env, "CHANGED=yes")
	require.Subset(t, ids, taskIDs(f.tasks(t, "job")), "idle template update must not schedule any task")
	f.unchanged(t, accepted, ids)
	f.deploy(t, desired, deployer.StackOptions{})
	f.unchanged(t, accepted, ids)
}

func TestSwarmCronjob116OnRealSwarm(t *testing.T) {
	f := newExternalSwarmFixture(t)
	// v1.16.0 internal/app uses cron.SecondOptional: five fields are supported.
	// One minute leaves time for bounded observations without scheduler churn.
	desired := f.load(t, 0, "echo scheduled-run; sleep 30", `        swarm.cronjob.enable: "true"
        swarm.cronjob.schedule: "* * * * *"
        swarm.cronjob.skip-running: "true"`)
	f.deploy(t, desired, deployer.StackOptions{})
	initial := f.inspect(t, "job")
	require.Zero(t, *initial.Spec.Mode.Replicated.Replicas)
	require.Empty(t, f.tasks(t, "job"))
	one := uint64(1)
	scheduler, err := f.sw.Services.Create(f.ctx, dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: f.stack + "_cron", Labels: map[string]string{"com.docker.stack.namespace": f.stack}},
		Mode:        dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &one}},
		TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Image: "crazymax/swarm-cronjob:1.16.0",
				Env:    []string{"LOG_LEVEL=debug", "LOG_JSON=false", "TZ=UTC"},
				Mounts: []mount.Mount{{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"}}},
			Placement: &dockerswarm.Placement{Constraints: []string{"node.role == manager"}},
		},
	}, dockerswarm.ServiceCreateOptions{})
	require.NoError(t, err)
	// Logs confirm discovery; service and task observations prove execution.
	f.poll(t, "real scheduler discovered job", func() bool {
		logs, logErr := f.sw.Services.Logs(f.ctx, swarm.NewServiceReference(f.stack, "cron"), swarm.ServiceLogsOptions{Limit: 200})
		joined := strings.Join(logs, "\n")
		return logErr == nil && strings.Contains(joined, f.stack+"_job") && strings.Contains(joined, "Add cronjob")
	})
	f.running(t, 1)
	external := f.settledJob(t)
	require.Equal(t, uint64(1), *external.Spec.Mode.Replicated.Replicas)
	require.Greater(t, external.Spec.TaskTemplate.ForceUpdate, initial.Spec.TaskTemplate.ForceUpdate)
	ids := taskIDs(f.tasks(t, "job"))
	require.Len(t, ids, 1)
	t.Logf("swarm-cronjob 1.16.0 executed task %s: replicas=1 forceUpdate=%d", ids[0], external.Spec.TaskTemplate.ForceUpdate)
	f.deploy(t, desired, deployer.StackOptions{})
	f.unchanged(t, external, ids)
	// Stop future ticks after proving a real scheduled execution. This isolates
	// any extra execution caused by synchronization from a legitimate cron tick.
	require.NoError(t, f.sw.Services.Remove(f.ctx, scheduler))
	for range 2 {
		f.deploy(t, desired, deployer.StackOptions{})
		f.unchanged(t, external, ids)
	}
	changed := f.load(t, 0, "echo changed; sleep 30", `        swarm.cronjob.enable: "true"
        swarm.cronjob.schedule: "* * * * *"
        swarm.cronjob.skip-running: "true"`)
	_, err = f.deployer.DeployStack(f.ctx, deployer.StackRequest{StackName: f.stack, SourceComposePath: f.path, Desired: changed})
	require.ErrorIs(t, err, services.ErrUpdateBlocked)
	require.Equal(t, "apply stack", deployer.FailedStep(err))
	f.unchanged(t, external, ids)
	f.poll(t, "scheduled job completed without interruption", func() bool {
		tasks := f.tasks(t, "job")
		return len(tasks) == 1 && tasks[0].ID == ids[0] && tasks[0].CurrentState == swarm.TaskStateComplete
	})
	f.deploy(t, desired, deployer.StackOptions{RetryFailedUpdates: true})
	f.unchanged(t, external, ids)
}

func TestManagedFailureWithExternalJobOnRealSwarm(t *testing.T) {
	f := newExternalSwarmFixture(t)
	desired := f.load(t, 1, "exit 23", "")
	f.deploy(t, desired, deployer.StackOptions{})
	f.poll(t, "historically failed external task", func() bool {
		tasks := f.tasks(t, "job")
		return len(tasks) == 1 && tasks[0].CurrentState == swarm.TaskStateFailed
	})
	job := f.settledJob(t)
	ids := taskIDs(f.tasks(t, "job"))
	// Reuse the Compose loader for the ordinary service and its rollout policy.
	require.NoError(t, os.WriteFile(f.path, []byte(`services:
  web:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 300"]
    deploy:
      restart_policy: {condition: none}
      update_config: {monitor: 1s, failure_action: pause}
`), 0o600))
	file, err := compose.NewFileLoader().Load(f.ctx, f.path)
	require.NoError(t, err)
	desired.Services = append(desired.Services, file.Compose.Services[0])
	f.deploy(t, desired, deployer.StackOptions{})
	// A genuine deployment-generated failure must still fail verification.
	desired.Services[1].Command = desired.Services[0].Command
	result, err := f.deployer.DeployStack(f.ctx, deployer.StackRequest{StackName: f.stack, SourceComposePath: f.path, Desired: desired})
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	require.Equal(t, "wait for service convergence", deployer.FailedStep(err))
	require.Equal(t, []string{"web"}, result.Verification.UnresolvedServices)
	desired.Services[1].Command = file.Compose.Services[0].Command
	f.deploy(t, desired, deployer.StackOptions{RetryFailedUpdates: true, UnresolvedServices: result.Verification.UnresolvedServices})
	f.unchanged(t, job, ids)
}
