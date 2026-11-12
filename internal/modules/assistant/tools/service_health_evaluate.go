package tools

import (
	"fmt"
	"slices"
	"sort"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	serviceHealthSeverityCritical = "critical"
	serviceHealthSeverityWarning  = "warning"
	serviceHealthSeverityInfo     = "info"

	serviceHealthPhaseTransient = "transient"
	serviceHealthPhaseActive    = "active"

	serviceHealthFlagServiceNotFound         = "service_not_found"
	serviceHealthFlagNoRunningTasks          = "no_running_tasks"
	serviceHealthFlagReplicasBelowDesired    = "replicas_below_desired"
	serviceHealthFlagNoEligibleNodes         = "no_eligible_nodes"
	serviceHealthFlagScaledToZero            = "scaled_to_zero"
	serviceHealthFlagRestartLoop             = "restart_loop"
	serviceHealthFlagUpdateRollbackCompleted = "update_rollback_completed"
	serviceHealthFlagUpdatePaused            = "update_paused"
	serviceHealthFlagUpdateInProgress        = "update_in_progress"

	serviceHealthReasonNodeUnavailable = "node_unavailable"
	serviceHealthReasonHealthcheck     = "healthcheck_failing"
	serviceHealthReasonCrashLoop       = "crash_loop"
	serviceHealthReasonTaskFailed      = "task_failed"
	serviceHealthReasonStarting        = "starting"
	serviceHealthReasonNoTasks         = "no_tasks"
	serviceHealthReasonUnknown         = "unknown"

	serviceModeReplicated    = "replicated"
	serviceModeGlobal        = "global"
	serviceModeReplicatedJob = "replicated-job"
	serviceModeGlobalJob     = "global-job"

	// serviceHealthMinimumGrace is the lower bound of the startup grace period.
	// Docker defaults UpdateConfig.Monitor to five seconds, which is shorter than most service startups.
	serviceHealthMinimumGrace = 30 * time.Second
	// serviceHealthRestartLoopThreshold is the number of current-revision failures that indicates a loop.
	serviceHealthRestartLoopThreshold = 3
	// serviceHealthRestartLoopRecency distinguishes ongoing flapping from failures that already stopped.
	serviceHealthRestartLoopRecency = 10 * time.Minute
	// serviceHealthUnhealthyMarker is the Docker task error fragment reported for failed healthchecks.
	serviceHealthUnhealthyMarker = "unhealthy container"
)

// serviceHealthStartingStates are non-terminal task states before running.
var serviceHealthStartingStates = []swarm.TaskState{
	swarm.TaskStateNew,
	swarm.TaskStateAllocated,
	swarm.TaskStatePending,
	swarm.TaskStateAssigned,
	swarm.TaskStateAccepted,
	swarm.TaskStatePreparing,
	swarm.TaskStateReady,
	swarm.TaskStateStarting,
}

// serviceHealthExitedStates are terminal task states that can carry a meaningful container exit code.
var serviceHealthExitedStates = []swarm.TaskState{
	swarm.TaskStateFailed,
	swarm.TaskStateShutdown,
	swarm.TaskStateComplete,
}

// serviceHealthFlag is one deterministic health conclusion.
type serviceHealthFlag struct {
	// Code identifies the condition.
	Code string `json:"code"`
	// Severity is critical, warning, or info.
	Severity string `json:"severity"`
	// Phase is transient for expected rollout or startup states, otherwise active.
	Phase string `json:"phase"`
	// Reason refines the condition, using faults codes when Docker reported a typed cause.
	Reason string `json:"reason,omitempty"`
	// Message is a short explanation and may contain untrusted text.
	Message string `json:"message"`
	// Since is the earliest evidence of the condition.
	Since *time.Time `json:"since,omitempty"`
	// SinceAgo is the elapsed time since Since, computed by the tool.
	SinceAgo string `json:"since_ago,omitempty"`
	// ExpectedReadyBy is when a transient condition is expected to settle.
	ExpectedReadyBy *time.Time `json:"expected_ready_by,omitempty"`
	// Evidence contains condition-specific facts.
	Evidence map[string]any `json:"evidence,omitempty"`
	// Refs identifies resources for detailed follow-up tools.
	Refs *serviceHealthRefs `json:"refs,omitempty"`
}

// serviceHealthRefs identifies resources accepted by detail tools.
type serviceHealthRefs struct {
	// TaskID is accepted by service_task_logs_get.
	TaskID string `json:"task_id,omitempty"`
	// DeploymentID is accepted by deployment_get.
	DeploymentID string `json:"deployment_id,omitempty"`
	// AlertID is accepted by alert_get.
	AlertID string `json:"alert_id,omitempty"`
}

// serviceHealthTaskResponse is a compact task projection.
type serviceHealthTaskResponse struct {
	// ID is accepted by service_task_logs_get.
	ID string `json:"id"`
	// Node is the Docker node identifier assigned to the task.
	Node string `json:"node,omitempty"`
	// State is the current Docker Swarm task state.
	State swarm.TaskState `json:"state"`
	// ExitCode is the container exit code for exited tasks when Docker reported one.
	ExitCode *int `json:"exit_code,omitempty"`
	// Error is the bounded task error reported by Docker and is untrusted data.
	Error string `json:"error,omitempty"`
	// UpdatedAt is the task last update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// serviceRuntimeAnalysis summarizes live tasks of one service.
type serviceRuntimeAnalysis struct {
	desired uint64
	running uint64
	// desiredTasks counts tasks of any revision that Swarm wants running.
	desiredTasks uint64
	// starting counts current-revision tasks that Swarm wants running and that have not started yet.
	starting int
	// unavailableNodeTasks counts running tasks reported on nodes that are not ready.
	unavailableNodeTasks int
	// failures contains failed or rejected current-revision tasks, newest first.
	failures   []swarm.ServiceTask
	fault      faults.Coded
	faultTask  swarm.ServiceTask
	reference  time.Time
	graceUntil time.Time
	inGrace    bool
}

func analyzeServiceRuntime(
	live *swarm.StackService,
	tasks []swarm.ServiceTask,
	readyNodes map[string]bool,
	now time.Time,
) serviceRuntimeAnalysis {
	analysis := serviceRuntimeAnalysis{reference: serviceHealthReference(live)}
	analysis.graceUntil = analysis.reference.Add(serviceHealthGrace(live.ServiceSpec))
	analysis.inGrace = now.Before(analysis.graceUntil)

	template := live.ServiceSpec.TaskTemplate
	for _, task := range newestServiceTasksFirst(tasks) {
		analysis.observeTask(task, template, readyNodes)
	}
	if analysis.fault == nil && len(analysis.failures) > 0 {
		if fault := analysis.failures[0].Fault(); fault != nil {
			analysis.fault = fault
			analysis.faultTask = analysis.failures[0]
		}
	}
	analysis.desired = serviceDesiredTaskCount(live, analysis.desiredTasks)

	return analysis
}

func (a *serviceRuntimeAnalysis) observeTask(
	task swarm.ServiceTask,
	template dockerswarm.TaskSpec,
	readyNodes map[string]bool,
) {
	current := task.Spec.CompareServiceTemplate(template).Equal()
	if task.DesiredState == swarm.TaskStateRunning {
		a.desiredTasks++
		a.observeDesiredTask(task, current, readyNodes)
	}
	if current && (task.CurrentState == swarm.TaskStateFailed || task.CurrentState == swarm.TaskStateRejected) {
		a.failures = append(a.failures, task)
	}
}

func (a *serviceRuntimeAnalysis) observeDesiredTask(task swarm.ServiceTask, current bool, readyNodes map[string]bool) {
	if task.CurrentState == swarm.TaskStateRunning {
		if serviceTaskNodeReady(task, readyNodes) {
			a.running++
		} else {
			a.unavailableNodeTasks++
		}
		return
	}
	if !current {
		return
	}
	if slices.Contains(serviceHealthStartingStates, task.CurrentState) {
		a.starting++
	}
	// A pending or rejected task Swarm still wants running explains the current deficit best.
	if a.fault == nil {
		if fault := task.Fault(); fault != nil {
			a.fault = fault
			a.faultTask = task
		}
	}
}

// hardEvidence reports failures that a rollout or startup cannot explain.
func (a *serviceRuntimeAnalysis) hardEvidence() bool {
	return a.fault != nil || a.unavailableNodeTasks > 0 || len(a.failures) >= serviceHealthRestartLoopThreshold
}

func (a *serviceRuntimeAnalysis) transient() bool {
	return a.inGrace && !a.hardEvidence()
}

// deficitReason classifies why fewer tasks run than desired, from the most to the least specific evidence.
func (a *serviceRuntimeAnalysis) deficitReason() (string, swarm.ServiceTask) {
	switch {
	case a.fault != nil:
		return string(a.fault.FaultCode()), a.faultTask
	case a.unavailableNodeTasks > 0:
		return serviceHealthReasonNodeUnavailable, swarm.ServiceTask{}
	case a.transient() && a.starting > 0:
		return serviceHealthReasonStarting, swarm.ServiceTask{}
	case len(a.failures) > 0:
		return serviceFailureReason(a.failures), a.failures[0]
	case a.starting > 0:
		return serviceHealthReasonStarting, swarm.ServiceTask{}
	case a.desiredTasks == 0:
		return serviceHealthReasonNoTasks, swarm.ServiceTask{}
	default:
		return serviceHealthReasonUnknown, swarm.ServiceTask{}
	}
}

func (a *serviceRuntimeAnalysis) applyPhase(flag *serviceHealthFlag) {
	if !a.transient() {
		flag.Phase = serviceHealthPhaseActive
		return
	}
	flag.Phase = serviceHealthPhaseTransient
	if flag.Severity == serviceHealthSeverityCritical {
		flag.Severity = serviceHealthSeverityWarning
	}
	readyBy := a.graceUntil
	flag.ExpectedReadyBy = &readyBy
}

// earliestEvidence returns the oldest current-revision failure, or the revision start when nothing failed.
func (a *serviceRuntimeAnalysis) earliestEvidence() time.Time {
	if len(a.failures) == 0 {
		return a.reference
	}

	return a.failures[len(a.failures)-1].UpdatedAt
}

func serviceRuntimeFlags(
	live *swarm.StackService,
	analysis *serviceRuntimeAnalysis,
	now time.Time,
) []serviceHealthFlag {
	if live.Mode == serviceModeReplicatedJob || live.Mode == serviceModeGlobalJob {
		return nil
	}
	if analysis.desired == 0 {
		return serviceZeroDesiredFlags(live, analysis)
	}
	if analysis.running < analysis.desired {
		return []serviceHealthFlag{serviceDeficitFlag(analysis)}
	}
	if flag, ok := serviceRestartLoopFlag(analysis, now); ok {
		return []serviceHealthFlag{flag}
	}

	return nil
}

func serviceZeroDesiredFlags(live *swarm.StackService, analysis *serviceRuntimeAnalysis) []serviceHealthFlag {
	since := analysis.reference
	if live.Mode == serviceModeReplicated {
		return []serviceHealthFlag{{
			Code:     serviceHealthFlagScaledToZero,
			Severity: serviceHealthSeverityInfo,
			Phase:    serviceHealthPhaseActive,
			Message:  "service is scaled to zero replicas",
			Since:    &since,
		}}
	}
	if live.Mode != serviceModeGlobal {
		return nil
	}
	flag := serviceHealthFlag{
		Code:     serviceHealthFlagNoEligibleNodes,
		Severity: serviceHealthSeverityWarning,
		Message:  "global service has no node eligible to run its tasks",
		Since:    &since,
	}
	analysis.applyPhase(&flag)

	return []serviceHealthFlag{flag}
}

func serviceDeficitFlag(analysis *serviceRuntimeAnalysis) serviceHealthFlag {
	reason, task := analysis.deficitReason()
	since := analysis.earliestEvidence()
	flag := serviceHealthFlag{
		Code:     serviceHealthFlagReplicasBelowDesired,
		Severity: serviceHealthSeverityWarning,
		Reason:   reason,
		Message:  fmt.Sprintf("%d of %d desired tasks are running", analysis.running, analysis.desired),
		Since:    &since,
		Evidence: map[string]any{
			"desired":                   analysis.desired,
			"running":                   analysis.running,
			"current_revision_failures": len(analysis.failures),
		},
	}
	if analysis.running == 0 {
		flag.Code = serviceHealthFlagNoRunningTasks
		flag.Severity = serviceHealthSeverityCritical
	}
	if analysis.unavailableNodeTasks > 0 {
		flag.Evidence["tasks_on_unavailable_nodes"] = analysis.unavailableNodeTasks
	}
	if task.ID != "" {
		flag.Evidence["task"] = serviceHealthTaskView(task)
		flag.Refs = &serviceHealthRefs{TaskID: task.ID}
	}
	analysis.applyPhase(&flag)

	return flag
}

// serviceRestartLoopFlag reports a service that runs all desired tasks while current-revision tasks keep failing.
func serviceRestartLoopFlag(analysis *serviceRuntimeAnalysis, now time.Time) (serviceHealthFlag, bool) {
	if len(analysis.failures) < serviceHealthRestartLoopThreshold {
		return serviceHealthFlag{}, false
	}
	latest := analysis.failures[0]
	if now.Sub(latest.UpdatedAt) > serviceHealthRestartLoopRecency {
		return serviceHealthFlag{}, false
	}
	since := analysis.earliestEvidence()

	return serviceHealthFlag{
		Code:     serviceHealthFlagRestartLoop,
		Severity: serviceHealthSeverityWarning,
		Phase:    serviceHealthPhaseActive,
		Reason:   serviceFailureReason(analysis.failures),
		Message: fmt.Sprintf(
			"%d current-revision tasks failed although all desired tasks are running now", len(analysis.failures),
		),
		Since: &since,
		Evidence: map[string]any{
			"current_revision_failures": len(analysis.failures),
			"task":                      serviceHealthTaskView(latest),
		},
		Refs: &serviceHealthRefs{TaskID: latest.ID},
	}, true
}

func serviceUpdateFlags(status *swarm.ServiceUpdateStatus) []serviceHealthFlag {
	if status == nil {
		return nil
	}
	flag := serviceHealthFlag{
		Phase:    serviceHealthPhaseActive,
		Message:  serviceHealthText(status.Message),
		Evidence: map[string]any{"update_state": string(status.State)},
	}
	since := status.StartedAt
	switch {
	case status.State == swarm.ServiceUpdateStateRollbackCompleted:
		flag.Code = serviceHealthFlagUpdateRollbackCompleted
		flag.Severity = serviceHealthSeverityWarning
		if !status.CompletedAt.IsZero() {
			since = status.CompletedAt
		}
	case status.State == swarm.ServiceUpdateStatePaused || status.State == swarm.ServiceUpdateStateRollbackPaused:
		flag.Code = serviceHealthFlagUpdatePaused
		flag.Severity = serviceHealthSeverityWarning
	case status.State == swarm.ServiceUpdateStateUpdating || status.State == swarm.ServiceUpdateStateRollbackStarted:
		flag.Code = serviceHealthFlagUpdateInProgress
		flag.Severity = serviceHealthSeverityInfo
	default:
		return nil
	}
	if !since.IsZero() {
		flag.Since = &since
	}

	return []serviceHealthFlag{flag}
}

func serviceFailureReason(failures []swarm.ServiceTask) string {
	if len(failures) == 0 {
		return ""
	}
	if serviceTaskFailedHealthcheck(failures[0]) {
		return serviceHealthReasonHealthcheck
	}
	if len(failures) >= serviceHealthRestartLoopThreshold {
		return serviceHealthReasonCrashLoop
	}

	return serviceHealthReasonTaskFailed
}

func serviceTaskFailedHealthcheck(task swarm.ServiceTask) bool {
	return containsFold(task.Error, serviceHealthUnhealthyMarker) ||
		containsFold(task.Message, serviceHealthUnhealthyMarker)
}

func serviceTaskNodeReady(task swarm.ServiceTask, readyNodes map[string]bool) bool {
	if readyNodes == nil {
		return true
	}

	return readyNodes[task.Node]
}

func serviceDesiredTaskCount(live *swarm.StackService, desiredTasks uint64) uint64 {
	if live.Mode == serviceModeReplicated {
		if live.Replicas == nil {
			return 0
		}
		return *live.Replicas
	}

	return max(live.DesiredTasks, desiredTasks)
}

// serviceHealthReference is when the current service revision started rolling out.
func serviceHealthReference(live *swarm.StackService) time.Time {
	reference := live.UpdatedAt
	if live.UpdateStatus != nil && live.UpdateStatus.StartedAt.After(reference) {
		reference = live.UpdateStatus.StartedAt
	}
	if reference.IsZero() {
		reference = live.CreatedAt
	}

	return reference
}

// serviceHealthGrace derives the startup grace period from the service update monitor and healthcheck.
func serviceHealthGrace(spec dockerswarm.ServiceSpec) time.Duration {
	grace := serviceHealthMinimumGrace
	if spec.UpdateConfig != nil {
		grace = max(grace, spec.UpdateConfig.Monitor)
	}
	if containerSpec := spec.TaskTemplate.ContainerSpec; containerSpec != nil && containerSpec.Healthcheck != nil {
		grace = max(grace, containerSpec.Healthcheck.StartPeriod)
	}

	return grace
}

func serviceHealthTaskView(task swarm.ServiceTask) serviceHealthTaskResponse {
	view := serviceHealthTaskResponse{
		ID:        task.ID,
		Node:      task.Node,
		State:     task.CurrentState,
		Error:     serviceHealthText(task.Error),
		UpdatedAt: task.UpdatedAt,
	}
	if task.ExitCode != nil && slices.Contains(serviceHealthExitedStates, task.CurrentState) {
		exitCode := *task.ExitCode
		view.ExitCode = &exitCode
	}

	return view
}

func newestServiceTasksFirst(tasks []swarm.ServiceTask) []swarm.ServiceTask {
	ordered := append([]swarm.ServiceTask(nil), tasks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].UpdatedAt.Equal(ordered[j].UpdatedAt) {
			return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
		}
		return ordered[i].ID > ordered[j].ID
	})

	return ordered
}
