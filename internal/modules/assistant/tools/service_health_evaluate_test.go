package tools

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

var serviceHealthTestNow = time.Date(2026, time.November, 12, 10, 0, 0, 0, time.UTC)

func serviceHealthTestTemplate(image string) dockerswarm.TaskSpec {
	return dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: image}}
}

func serviceHealthTestLive(replicas uint64, updatedAgo time.Duration) *swarm.StackService {
	return &swarm.StackService{
		ID:        "svc-api",
		Name:      "api",
		Stack:     "shop",
		FullName:  "shop_api",
		Mode:      serviceModeReplicated,
		Replicas:  &replicas,
		CreatedAt: serviceHealthTestNow.Add(-24 * time.Hour),
		UpdatedAt: serviceHealthTestNow.Add(-updatedAgo),
		ServiceSpec: dockerswarm.ServiceSpec{
			TaskTemplate: serviceHealthTestTemplate("app:2"),
			Mode: dockerswarm.ServiceMode{
				Replicated: &dockerswarm.ReplicatedService{Replicas: &replicas},
			},
		},
	}
}

func serviceHealthTestTask(
	id string,
	slot int,
	template dockerswarm.TaskSpec,
	state swarm.TaskState,
	updatedAgo time.Duration,
) swarm.ServiceTask {
	updatedAt := serviceHealthTestNow.Add(-updatedAgo)
	desired := swarm.TaskStateShutdown
	if state == swarm.TaskStateRunning || state == swarm.TaskStatePending || state == swarm.TaskStateStarting {
		desired = swarm.TaskStateRunning
	}

	return swarm.ServiceTask{
		ID:           id,
		Node:         "node-1",
		Slot:         slot,
		DesiredState: desired,
		CurrentState: state,
		CreatedAt:    updatedAt.Add(-time.Minute),
		UpdatedAt:    updatedAt,
		Spec:         swarm.NewTaskSpec(template),
	}
}

func serviceHealthTestFailed(id string, slot int, template dockerswarm.TaskSpec, updatedAgo time.Duration) swarm.ServiceTask {
	task := serviceHealthTestTask(id, slot, template, swarm.TaskStateFailed, updatedAgo)
	exitCode := 137
	task.ExitCode = &exitCode
	task.Error = "task: non-zero exit (137)"

	return task
}

func serviceHealthTestInput(live *swarm.StackService, tasks ...swarm.ServiceTask) *serviceHealthInput {
	return &serviceHealthInput{
		stack:   "shop",
		service: "api",
		now:     serviceHealthTestNow,
		sources: serviceHealthSources{
			Swarm:       serviceHealthSourceOK,
			Nodes:       serviceHealthSourceOK,
			Alerts:      serviceHealthSourceOK,
			Deployments: serviceHealthSourceOK,
		},
		live:          live,
		tasks:         tasks,
		readyNodes:    map[string]bool{"node-1": true},
		deploymentIDs: map[string]bool{},
	}
}

func findServiceHealthFlag(t *testing.T, flags []serviceHealthFlag, code string) serviceHealthFlag {
	t.Helper()
	for _, flag := range flags {
		if flag.Code == code {
			return flag
		}
	}
	require.Failf(t, "flag not found", "code %s in %+v", code, flags)

	return serviceHealthFlag{}
}

func TestServiceHealthHealthyService(t *testing.T) {
	t.Parallel()

	live := serviceHealthTestLive(3, time.Hour)
	template := live.ServiceSpec.TaskTemplate
	response := buildServiceHealthResponse(serviceHealthTestInput(live,
		serviceHealthTestTask("r1", 1, template, swarm.TaskStateRunning, time.Hour),
		serviceHealthTestTask("r2", 2, template, swarm.TaskStateRunning, time.Hour),
		serviceHealthTestTask("r3", 3, template, swarm.TaskStateRunning, time.Hour),
	))

	assert.Equal(t, serviceHealthStatusHealthy, response.Status)
	assert.Empty(t, response.Flags)
	require.NotNil(t, response.Service)
	assert.Equal(t, uint64(3), response.Service.Desired)
	assert.Equal(t, uint64(3), response.Service.Running)
	require.NotNil(t, response.Tasks)
	assert.Zero(t, response.Tasks.CurrentRevisionFailures)
	assert.Empty(t, response.Tasks.RecentFailures)
}

func TestServiceHealthCrashLoopWithFailedDeployment(t *testing.T) {
	t.Parallel()

	live := serviceHealthTestLive(2, time.Hour)
	template := live.ServiceSpec.TaskTemplate
	input := serviceHealthTestInput(live,
		serviceHealthTestFailed("f3", 1, template, time.Minute),
		serviceHealthTestFailed("f2", 2, template, 2*time.Minute),
		serviceHealthTestFailed("f1", 1, template, 3*time.Minute),
		serviceHealthTestFailed("old", 1, serviceHealthTestTemplate("app:1"), 30*time.Minute),
	)
	finishedAt := serviceHealthTestNow.Add(-5 * time.Minute)
	after := "app:2"
	input.latest = &deploymentmodel.Deployment{
		ID: "dep-2", Commit: "abc123", Status: deploymentmodel.Failed, Phase: deploymentmodel.PhaseVerification,
		ErrorCode: "convergence_failed", StartedAt: finishedAt.Add(-time.Minute), FinishedAt: &finishedAt,
		AttemptCount: 1,
		Changes: []deploymentmodel.Change{
			{ResourceType: "service", ResourceName: "api", Field: "image", After: &after},
			{ResourceType: "service", ResourceName: "worker", Field: "image", After: &after},
		},
	}

	response := buildServiceHealthResponse(input)

	assert.Equal(t, serviceHealthStatusFailing, response.Status)
	require.NotEmpty(t, response.Flags)
	assert.Equal(t, serviceHealthSeverityCritical, response.Flags[0].Severity, "critical flags are ordered first")

	down := findServiceHealthFlag(t, response.Flags, serviceHealthFlagNoRunningTasks)
	assert.Equal(t, serviceHealthSeverityCritical, down.Severity)
	assert.Equal(t, serviceHealthPhaseActive, down.Phase)
	assert.Equal(t, serviceHealthReasonCrashLoop, down.Reason)
	assert.Equal(t, 3, down.Evidence["current_revision_failures"])
	require.NotNil(t, down.Refs)
	assert.Equal(t, "f3", down.Refs.TaskID)
	require.NotNil(t, down.Since)
	assert.Equal(t, serviceHealthTestNow.Add(-3*time.Minute), *down.Since)
	assert.Equal(t, "3m", down.SinceAgo)
	assert.Nil(t, down.ExpectedReadyBy)

	failedDeploy := findServiceHealthFlag(t, response.Flags, serviceHealthFlagLastDeployFailed)
	assert.Equal(t, serviceHealthSeverityCritical, failedDeploy.Severity, "a failed deploy of a down service is critical")
	assert.Equal(t, "convergence_failed", failedDeploy.Reason)
	assert.Equal(t, "dep-2", failedDeploy.Refs.DeploymentID)

	require.NotNil(t, response.Tasks)
	assert.Equal(t, 3, response.Tasks.CurrentRevisionFailures, "failures of older revisions are ignored")
	require.Len(t, response.Tasks.RecentFailures, 3)
	require.NotNil(t, response.Tasks.RecentFailures[0].ExitCode)
	assert.Equal(t, 137, *response.Tasks.RecentFailures[0].ExitCode)

	require.Len(t, response.Deployments, 1)
	assert.Equal(t, serviceHealthDeploymentRoleLatest, response.Deployments[0].Role)
	assert.True(t, response.Deployments[0].ServiceChanged)
	require.Len(t, response.Deployments[0].ServiceChanges, 1)
	assert.Equal(t, "api", response.Deployments[0].ServiceChanges[0].ResourceName)
}

func TestServiceHealthTransientStartup(t *testing.T) {
	t.Parallel()

	live := serviceHealthTestLive(1, 10*time.Second)
	response := buildServiceHealthResponse(serviceHealthTestInput(live,
		serviceHealthTestTask("s1", 1, live.ServiceSpec.TaskTemplate, swarm.TaskStateStarting, 5*time.Second),
	))

	assert.Equal(t, serviceHealthStatusDegraded, response.Status)
	flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagNoRunningTasks)
	assert.Equal(t, serviceHealthSeverityWarning, flag.Severity, "transient flags are capped at warning")
	assert.Equal(t, serviceHealthPhaseTransient, flag.Phase)
	assert.Equal(t, serviceHealthReasonStarting, flag.Reason)
	require.NotNil(t, flag.ExpectedReadyBy)
	assert.Equal(t, live.UpdatedAt.Add(serviceHealthMinimumGrace), *flag.ExpectedReadyBy)
}

func TestServiceHealthHardEvidenceOverridesGrace(t *testing.T) {
	t.Parallel()

	live := serviceHealthTestLive(1, 10*time.Second)
	pending := serviceHealthTestTask("p1", 1, live.ServiceSpec.TaskTemplate, swarm.TaskStatePending, 5*time.Second)
	pending.Error = "no suitable node (scheduling constraints not satisfied on 3 nodes)"

	response := buildServiceHealthResponse(serviceHealthTestInput(live, pending))

	assert.Equal(t, serviceHealthStatusFailing, response.Status)
	flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagNoRunningTasks)
	assert.Equal(t, serviceHealthPhaseActive, flag.Phase)
	assert.Equal(t, string(faults.CodeNoSuitableNode), flag.Reason)
	assert.Equal(t, "p1", flag.Refs.TaskID)
	assert.Nil(t, flag.ExpectedReadyBy)
}

func TestServiceHealthRunningTaskOnUnavailableNodeIsNotCounted(t *testing.T) {
	t.Parallel()

	live := serviceHealthTestLive(1, time.Hour)
	running := serviceHealthTestTask("r1", 1, live.ServiceSpec.TaskTemplate, swarm.TaskStateRunning, time.Hour)
	running.Node = "node-down"
	input := serviceHealthTestInput(live, running)
	input.readyNodes = map[string]bool{"node-1": true, "node-down": false}

	response := buildServiceHealthResponse(input)

	flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagNoRunningTasks)
	assert.Equal(t, serviceHealthReasonNodeUnavailable, flag.Reason)
	assert.Equal(t, 1, flag.Evidence["tasks_on_unavailable_nodes"])
	assert.Equal(t, uint64(0), response.Service.Running)
}

func TestServiceHealthRestartLoopRequiresRecentFailures(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		failedAgo   time.Duration
		expectFlag  bool
		expectState string
	}{
		{name: "ongoing", failedAgo: time.Minute, expectFlag: true, expectState: serviceHealthStatusDegraded},
		{name: "stopped", failedAgo: 20 * time.Minute, expectState: serviceHealthStatusHealthy},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			live := serviceHealthTestLive(2, time.Hour)
			template := live.ServiceSpec.TaskTemplate
			response := buildServiceHealthResponse(serviceHealthTestInput(live,
				serviceHealthTestTask("r1", 1, template, swarm.TaskStateRunning, 30*time.Second),
				serviceHealthTestTask("r2", 2, template, swarm.TaskStateRunning, time.Hour),
				serviceHealthTestFailed("f3", 1, template, testCase.failedAgo),
				serviceHealthTestFailed("f2", 1, template, testCase.failedAgo+time.Minute),
				serviceHealthTestFailed("f1", 1, template, testCase.failedAgo+2*time.Minute),
			))

			assert.Equal(t, testCase.expectState, response.Status)
			if !testCase.expectFlag {
				assert.Empty(t, response.Flags)
				return
			}
			flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagRestartLoop)
			assert.Equal(t, serviceHealthReasonCrashLoop, flag.Reason)
			assert.Equal(t, "f3", flag.Refs.TaskID)
		})
	}
}

func TestServiceHealthUpdateFlags(t *testing.T) {
	t.Parallel()

	startedAt := serviceHealthTestNow.Add(-10 * time.Minute)
	completedAt := serviceHealthTestNow.Add(-5 * time.Minute)
	testCases := []struct {
		name     string
		state    swarm.ServiceUpdateState
		code     string
		severity string
		since    time.Time
	}{
		{name: "rollback completed", state: swarm.ServiceUpdateStateRollbackCompleted,
			code: serviceHealthFlagUpdateRollbackCompleted, severity: serviceHealthSeverityWarning, since: completedAt},
		{name: "paused", state: swarm.ServiceUpdateStatePaused,
			code: serviceHealthFlagUpdatePaused, severity: serviceHealthSeverityWarning, since: startedAt},
		{name: "rollback paused", state: swarm.ServiceUpdateStateRollbackPaused,
			code: serviceHealthFlagUpdatePaused, severity: serviceHealthSeverityWarning, since: startedAt},
		{name: "updating", state: swarm.ServiceUpdateStateUpdating,
			code: serviceHealthFlagUpdateInProgress, severity: serviceHealthSeverityInfo, since: startedAt},
		{name: "completed", state: swarm.ServiceUpdateStateCompleted},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			flags := serviceUpdateFlags(&swarm.ServiceUpdateStatus{
				State: testCase.state, StartedAt: startedAt, CompletedAt: completedAt, Message: "update message",
			})
			if testCase.code == "" {
				assert.Empty(t, flags)
				return
			}
			require.Len(t, flags, 1)
			assert.Equal(t, testCase.code, flags[0].Code)
			assert.Equal(t, testCase.severity, flags[0].Severity)
			require.NotNil(t, flags[0].Since)
			assert.Equal(t, testCase.since, *flags[0].Since)
			assert.Equal(t, "update message", flags[0].Message)
		})
	}
}

func TestServiceHealthZeroDesiredAndJobs(t *testing.T) {
	t.Parallel()

	scaledDown := serviceHealthTestLive(0, time.Hour)
	response := buildServiceHealthResponse(serviceHealthTestInput(scaledDown))
	assert.Equal(t, serviceHealthStatusHealthy, response.Status)
	flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagScaledToZero)
	assert.Equal(t, serviceHealthSeverityInfo, flag.Severity)

	job := serviceHealthTestLive(1, time.Hour)
	job.Mode = serviceModeReplicatedJob
	response = buildServiceHealthResponse(serviceHealthTestInput(job))
	assert.Equal(t, serviceHealthStatusHealthy, response.Status)
	assert.Empty(t, response.Flags)
}

func TestServiceHealthSwarmUnavailableIsUnknown(t *testing.T) {
	t.Parallel()

	input := serviceHealthTestInput(nil)
	input.sources.Swarm = serviceHealthSourceUnavailable
	input.sources.Nodes = serviceHealthSourceSkipped
	input.latest = &deploymentmodel.Deployment{
		ID: "dep-1", Status: deploymentmodel.Failed, StartedAt: serviceHealthTestNow.Add(-time.Hour),
	}

	response := buildServiceHealthResponse(input)

	assert.Equal(t, serviceHealthStatusUnknown, response.Status)
	assert.Nil(t, response.Service)
	flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagLastDeployFailed)
	assert.Equal(t, serviceHealthSeverityWarning, flag.Severity, "service health is not known to be down")
}

func TestServiceHealthServiceMissingFromSwarm(t *testing.T) {
	t.Parallel()

	input := serviceHealthTestInput(nil)
	input.latest = &deploymentmodel.Deployment{
		ID: "dep-1", Status: deploymentmodel.Failed, StartedAt: serviceHealthTestNow.Add(-time.Hour),
	}

	response := buildServiceHealthResponse(input)

	assert.Equal(t, serviceHealthStatusFailing, response.Status)
	findServiceHealthFlag(t, response.Flags, serviceHealthFlagServiceNotFound)
	flag := findServiceHealthFlag(t, response.Flags, serviceHealthFlagLastDeployFailed)
	assert.Equal(t, serviceHealthSeverityCritical, flag.Severity)
}

func TestServiceHealthAlertsAndConvergence(t *testing.T) {
	t.Parallel()

	live := serviceHealthTestLive(1, time.Hour)
	input := serviceHealthTestInput(live,
		serviceHealthTestTask("r1", 1, live.ServiceSpec.TaskTemplate, swarm.TaskStateRunning, time.Hour),
	)
	input.latest = &deploymentmodel.Deployment{
		ID: "dep-3", Status: deploymentmodel.Succeeded, StartedAt: serviceHealthTestNow.Add(-time.Hour),
		Verification: deploymentmodel.Verification{Known: true, UnresolvedServices: []string{"api"}},
	}
	openedAt := serviceHealthTestNow.Add(-2 * time.Hour)
	input.alerts = []serviceHealthAlert{
		{
			alert: alertmodel.Alert{
				ID: "alert-drift", Kind: alertmodel.AlertKindServiceDrift, Title: "Service drifted",
				OpenedAt: openedAt, UpdatedAt: openedAt,
			},
			scope:  alertmodel.ResourceTypeService,
			linked: true,
		},
		{
			alert: alertmodel.Alert{
				ID: "alert-sync", Kind: alertmodel.AlertKindSyncFailed, Title: "Sync failed",
				DeploymentID: "dep-3", OpenedAt: openedAt, UpdatedAt: openedAt,
				Findings: []alertmodel.Finding{{Code: faults.CodeImagePullDenied, Summary: "denied", DetectedAt: openedAt}},
			},
			scope:  alertmodel.ResourceTypeStack,
			linked: true,
		},
	}

	response := buildServiceHealthResponse(input)

	assert.Equal(t, serviceHealthStatusDegraded, response.Status)
	drift := findServiceHealthFlag(t, response.Flags, serviceHealthFlagDriftDetected)
	assert.Equal(t, "alert-drift", drift.Refs.AlertID)
	sync := findServiceHealthFlag(t, response.Flags, serviceHealthFlagOpenAlert)
	assert.Equal(t, []faults.Code{faults.CodeImagePullDenied}, sync.Evidence["finding_codes"])
	assert.Equal(t, "dep-3", sync.Refs.DeploymentID)
	findServiceHealthFlag(t, response.Flags, serviceHealthFlagConvergenceUnverified)

	require.Len(t, response.Alerts, 2)
	require.Len(t, response.Alerts[1].Findings, 1)
	assert.Equal(t, faults.CodeImagePullDenied, response.Alerts[1].Findings[0].Code)
}

func TestServiceHealthGrace(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		spec     dockerswarm.ServiceSpec
		expected time.Duration
	}{
		{name: "defaults", expected: serviceHealthMinimumGrace},
		{
			name:     "short monitor",
			spec:     dockerswarm.ServiceSpec{UpdateConfig: &dockerswarm.UpdateConfig{Monitor: 5 * time.Second}},
			expected: serviceHealthMinimumGrace,
		},
		{
			name:     "long monitor",
			spec:     dockerswarm.ServiceSpec{UpdateConfig: &dockerswarm.UpdateConfig{Monitor: time.Minute}},
			expected: time.Minute,
		},
		{
			name: "healthcheck start period",
			spec: dockerswarm.ServiceSpec{
				UpdateConfig: &dockerswarm.UpdateConfig{Monitor: time.Minute},
				TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{
					Healthcheck: &container.HealthConfig{StartPeriod: 2 * time.Minute},
				}},
			},
			expected: 2 * time.Minute,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.expected, serviceHealthGrace(testCase.spec))
		})
	}
}

func TestServiceHealthTaskViewExitCodeOnlyForExitedTasks(t *testing.T) {
	t.Parallel()

	exitCode := 0
	running := swarm.ServiceTask{ID: "r1", CurrentState: swarm.TaskStateRunning, ExitCode: &exitCode}
	assert.Nil(t, serviceHealthTaskView(running).ExitCode)

	failedCode := 1
	failed := swarm.ServiceTask{ID: "f1", CurrentState: swarm.TaskStateFailed, ExitCode: &failedCode}
	require.NotNil(t, serviceHealthTaskView(failed).ExitCode)
	assert.Equal(t, 1, *serviceHealthTaskView(failed).ExitCode)
}

func TestServiceHealthAgo(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		elapsed  time.Duration
		expected string
	}{
		{elapsed: -time.Second, expected: "0s"},
		{elapsed: 42 * time.Second, expected: "42s"},
		{elapsed: 12 * time.Minute, expected: "12m"},
		{elapsed: 5 * time.Hour, expected: "5h"},
		{elapsed: 72 * time.Hour, expected: "3d"},
	}
	for _, testCase := range testCases {
		assert.Equal(t, testCase.expected, serviceHealthAgo(serviceHealthTestNow, serviceHealthTestNow.Add(-testCase.elapsed)))
	}
}
