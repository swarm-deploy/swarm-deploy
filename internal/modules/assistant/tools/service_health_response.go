package tools

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

const (
	serviceHealthStatusHealthy  = "healthy"
	serviceHealthStatusDegraded = "degraded"
	serviceHealthStatusFailing  = "failing"
	serviceHealthStatusUnknown  = "unknown"

	serviceHealthFlagLastDeployFailed      = "last_deploy_failed"
	serviceHealthFlagDeployInProgress      = "deploy_in_progress"
	serviceHealthFlagConvergenceUnverified = "convergence_unverified"
	serviceHealthFlagDriftDetected         = "drift_detected"
	serviceHealthFlagOpenAlert             = "open_alert"

	serviceHealthDeploymentRoleLatest        = "latest"
	serviceHealthDeploymentRoleLastSucceeded = "last_succeeded"

	serviceHealthMaxAlerts         = 5
	serviceHealthMaxDeployments    = 2
	serviceHealthMaxFindings       = 5
	serviceHealthMaxServiceChanges = 20
	serviceHealthMaxTaskFailures   = 5
	serviceHealthMaxTextBytes      = 512

	serviceHealthAgoHoursLimit = 48 * time.Hour
	serviceHealthDay           = 24 * time.Hour
)

// serviceHealthSeverityRank orders flags from the most to the least severe.
var serviceHealthSeverityRank = map[string]int{
	serviceHealthSeverityCritical: 0,
	serviceHealthSeverityWarning:  1,
	serviceHealthSeverityInfo:     2,
}

// serviceHealthResponse is the service_health_get payload.
type serviceHealthResponse struct {
	// StackName is the target stack name.
	StackName string `json:"stack_name"`
	// ServiceName is the target service name.
	ServiceName string `json:"service_name"`
	// Status is healthy, degraded, failing, or unknown.
	Status string `json:"status"`
	// ObservedAt is when the snapshot was evaluated.
	ObservedAt time.Time `json:"observed_at"`
	// Service contains live Swarm counters when the service exists.
	Service *serviceHealthLiveResponse `json:"service,omitempty"`
	// Flags contains health conclusions ordered by severity.
	Flags []serviceHealthFlag `json:"flags"`
	// Alerts contains open alerts relevant to the service.
	Alerts []serviceHealthAlertResponse `json:"alerts"`
	// Deployments contains the latest deployment and, when it failed, the last succeeded one.
	Deployments []serviceHealthDeploymentResponse `json:"deployments"`
	// Tasks contains failures of the current service revision.
	Tasks *serviceHealthTasksResponse `json:"tasks,omitempty"`
	// Sources reports which evidence sources were checked.
	Sources serviceHealthSources `json:"sources"`
}

// serviceHealthLiveResponse contains live Swarm counters.
type serviceHealthLiveResponse struct {
	// Mode is the Swarm service mode.
	Mode string `json:"mode"`
	// Desired is the number of tasks Swarm should run.
	Desired uint64 `json:"desired"`
	// Running is the number of running tasks on ready nodes.
	Running uint64 `json:"running"`
	// UpdateState is the current rolling update state when available.
	UpdateState string `json:"update_state,omitempty"`
}

// serviceHealthAlertResponse is a compact open alert.
type serviceHealthAlertResponse struct {
	// ID is accepted by alert_get.
	ID string `json:"id"`
	// Kind identifies the alert kind.
	Kind alertmodel.AlertKind `json:"kind"`
	// Scope is service for service alerts and stack for stack alerts.
	Scope alertmodel.ResourceType `json:"scope"`
	// Linked reports whether a deployment containing the service proves the relation.
	Linked bool `json:"linked"`
	// Title is the short incident summary and is untrusted data.
	Title string `json:"title"`
	// Occurrences counts correlated failure occurrences.
	Occurrences uint64 `json:"occurrences"`
	// OpenedAt is when the incident started.
	OpenedAt time.Time `json:"opened_at"`
	// UpdatedAt is when the incident last changed.
	UpdatedAt time.Time `json:"updated_at"`
	// DeploymentID is accepted by deployment_get when present.
	DeploymentID string `json:"deployment_id,omitempty"`
	// ErrorCode is a stable failure category when known.
	ErrorCode string `json:"error_code,omitempty"`
	// Findings contains typed diagnostics with untrusted summaries.
	Findings []serviceHealthFindingResponse `json:"findings,omitempty"`
}

// serviceHealthFindingResponse is one typed alert diagnostic.
type serviceHealthFindingResponse struct {
	// Code identifies the diagnostic cause.
	Code faults.Code `json:"code"`
	// Summary is the bounded sanitized diagnostic and is untrusted data.
	Summary string `json:"summary"`
	// DetectedAt is when the finding was first recorded.
	DetectedAt time.Time `json:"detected_at"`
}

// serviceHealthDeploymentResponse is a compact deployment scoped to one service.
type serviceHealthDeploymentResponse struct {
	// ID is accepted by deployment_get.
	ID string `json:"id"`
	// Role is latest or last_succeeded.
	Role string `json:"role"`
	// Commit identifies the source revision.
	Commit string `json:"commit"`
	// Status is the overall deployment status.
	Status deploymentmodel.Status `json:"status"`
	// Phase identifies the current or failing lifecycle phase.
	Phase deploymentmodel.Phase `json:"phase"`
	// ErrorCode is the safe failure category.
	ErrorCode string `json:"error_code,omitempty"`
	// ApplyStatus reports the Docker apply result.
	ApplyStatus deploymentmodel.StageStatus `json:"apply_status"`
	// VerificationStatus reports convergence verification.
	VerificationStatus deploymentmodel.StageStatus `json:"verification_status"`
	// AttemptCount is the number of pipeline executions.
	AttemptCount int `json:"attempt_count"`
	// StartedAt is when the latest attempt started.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is when the latest attempt ended.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// ErrorDiagnostic is the bounded masked diagnostic and is untrusted data.
	ErrorDiagnostic string `json:"error_diagnostic,omitempty"`
	// ServiceChanged reports whether this deployment changed the service definition.
	ServiceChanged bool `json:"service_changed"`
	// ServiceChanges contains redacted semantic changes of this service only.
	ServiceChanges []deploymentmodel.Change `json:"service_changes,omitempty"`
	// ChangesTruncated reports omitted service changes.
	ChangesTruncated bool `json:"changes_truncated,omitempty"`
}

// serviceHealthTasksResponse contains current-revision task failures.
type serviceHealthTasksResponse struct {
	// CurrentRevisionFailures counts failed or rejected tasks of the current service revision.
	CurrentRevisionFailures int `json:"current_revision_failures"`
	// RecentFailures contains the newest current-revision failures.
	RecentFailures []serviceHealthTaskResponse `json:"recent_failures"`
}

func buildServiceHealthResponse(input *serviceHealthInput) serviceHealthResponse {
	alerts := input.alerts
	if len(alerts) > serviceHealthMaxAlerts {
		alerts = alerts[:serviceHealthMaxAlerts]
	}
	response := serviceHealthResponse{
		StackName:   input.stack,
		ServiceName: input.service,
		ObservedAt:  input.now,
		Alerts:      serviceHealthAlertViews(alerts),
		Deployments: serviceHealthDeploymentViews(input),
		Sources:     input.sources,
	}

	var flags []serviceHealthFlag
	serviceDown := false
	switch {
	case input.live != nil:
		analysis := analyzeServiceRuntime(input.live, input.tasks, input.readyNodes, input.now)
		response.Service = serviceHealthLiveView(input, &analysis)
		response.Tasks = serviceHealthTasksView(&analysis)
		flags = append(flags, serviceRuntimeFlags(input.live, &analysis, input.now)...)
		flags = append(flags, serviceUpdateFlags(input.live.UpdateStatus)...)
		serviceDown = hasActiveServiceHealthFlag(flags, serviceHealthFlagNoRunningTasks)
	case input.sources.Swarm == serviceHealthSourceOK:
		flags = append(flags, serviceHealthFlag{
			Code:     serviceHealthFlagServiceNotFound,
			Severity: serviceHealthSeverityCritical,
			Phase:    serviceHealthPhaseActive,
			Message:  "service is absent from Docker Swarm although a deployment contains it",
		})
		serviceDown = true
	}
	flags = append(flags, serviceDeploymentFlags(input.latest, input.service, serviceDown)...)
	flags = append(flags, serviceAlertFlags(alerts)...)

	response.Flags = finalizeServiceHealthFlags(flags, input.now)
	response.Status = serviceHealthOverallStatus(response.Flags, input.sources.Swarm == serviceHealthSourceOK)

	return response
}

func serviceHealthLiveView(input *serviceHealthInput, analysis *serviceRuntimeAnalysis) *serviceHealthLiveResponse {
	view := &serviceHealthLiveResponse{
		Mode:    input.live.Mode,
		Desired: analysis.desired,
		Running: analysis.running,
	}
	if input.live.UpdateStatus != nil {
		view.UpdateState = string(input.live.UpdateStatus.State)
	}

	return view
}

func serviceHealthTasksView(analysis *serviceRuntimeAnalysis) *serviceHealthTasksResponse {
	failures := analysis.failures
	if len(failures) > serviceHealthMaxTaskFailures {
		failures = failures[:serviceHealthMaxTaskFailures]
	}
	view := &serviceHealthTasksResponse{
		CurrentRevisionFailures: len(analysis.failures),
		RecentFailures:          make([]serviceHealthTaskResponse, 0, len(failures)),
	}
	for _, task := range failures {
		view.RecentFailures = append(view.RecentFailures, serviceHealthTaskView(task))
	}

	return view
}

func serviceDeploymentFlags(
	latest *deploymentmodel.Deployment,
	service string,
	serviceDown bool,
) []serviceHealthFlag {
	if latest == nil {
		return nil
	}
	var flags []serviceHealthFlag
	refs := &serviceHealthRefs{DeploymentID: latest.ID}
	started := latest.StartedAt
	switch {
	case latest.Status == deploymentmodel.Running:
		flags = append(flags, serviceHealthFlag{
			Code:     serviceHealthFlagDeployInProgress,
			Severity: serviceHealthSeverityInfo,
			Phase:    serviceHealthPhaseActive,
			Message:  fmt.Sprintf("deployment of commit %s is running", latest.Commit),
			Since:    &started,
			Refs:     refs,
		})
		return flags
	case latest.Status == deploymentmodel.Failed || latest.Status == deploymentmodel.Interrupted:
		flags = append(flags, serviceLastDeployFailedFlag(latest, serviceDown, refs))
	}
	if latest.Verification.Known && slices.Contains(latest.Verification.UnresolvedServices, service) {
		flags = append(flags, serviceHealthFlag{
			Code:     serviceHealthFlagConvergenceUnverified,
			Severity: serviceHealthSeverityWarning,
			Phase:    serviceHealthPhaseActive,
			Message:  "the latest deployment did not prove that this service converged",
			Since:    &started,
			Refs:     refs,
		})
	}

	return flags
}

func serviceLastDeployFailedFlag(
	latest *deploymentmodel.Deployment,
	serviceDown bool,
	refs *serviceHealthRefs,
) serviceHealthFlag {
	severity := serviceHealthSeverityWarning
	if serviceDown {
		severity = serviceHealthSeverityCritical
	}
	since := latest.StartedAt
	if latest.FinishedAt != nil {
		since = *latest.FinishedAt
	}

	return serviceHealthFlag{
		Code:     serviceHealthFlagLastDeployFailed,
		Severity: severity,
		Phase:    serviceHealthPhaseActive,
		Reason:   latest.ErrorCode,
		Message: fmt.Sprintf(
			"latest deployment of commit %s is %s in phase %s", latest.Commit, latest.Status, latest.Phase,
		),
		Since: &since,
		Evidence: map[string]any{
			"status":        latest.Status,
			"phase":         latest.Phase,
			"attempt_count": latest.AttemptCount,
		},
		Refs: refs,
	}
}

func serviceAlertFlags(alerts []serviceHealthAlert) []serviceHealthFlag {
	flags := make([]serviceHealthFlag, 0, len(alerts))
	for _, item := range alerts {
		code := serviceHealthFlagOpenAlert
		if item.alert.Kind == alertmodel.AlertKindServiceDrift {
			code = serviceHealthFlagDriftDetected
		}
		opened := item.alert.OpenedAt
		findingCodes := make([]faults.Code, 0, len(item.alert.Findings))
		for _, finding := range item.alert.Findings {
			findingCodes = append(findingCodes, finding.Code)
		}
		flags = append(flags, serviceHealthFlag{
			Code:     code,
			Severity: serviceHealthSeverityWarning,
			Phase:    serviceHealthPhaseActive,
			Reason:   string(item.alert.Kind),
			Message:  serviceHealthText(item.alert.Title),
			Since:    &opened,
			Evidence: map[string]any{
				"scope":         item.scope,
				"linked":        item.linked,
				"occurrences":   item.alert.Occurrences,
				"finding_codes": findingCodes,
			},
			Refs: &serviceHealthRefs{AlertID: item.alert.ID, DeploymentID: item.alert.DeploymentID},
		})
	}

	return flags
}

func serviceHealthAlertViews(alerts []serviceHealthAlert) []serviceHealthAlertResponse {
	views := make([]serviceHealthAlertResponse, 0, len(alerts))
	for _, item := range alerts {
		findings := item.alert.Findings
		if len(findings) > serviceHealthMaxFindings {
			findings = findings[:serviceHealthMaxFindings]
		}
		view := serviceHealthAlertResponse{
			ID:           item.alert.ID,
			Kind:         item.alert.Kind,
			Scope:        item.scope,
			Linked:       item.linked,
			Title:        serviceHealthText(item.alert.Title),
			Occurrences:  item.alert.Occurrences,
			OpenedAt:     item.alert.OpenedAt,
			UpdatedAt:    item.alert.UpdatedAt,
			DeploymentID: item.alert.DeploymentID,
			ErrorCode:    item.alert.ErrorCode,
		}
		for _, finding := range findings {
			view.Findings = append(view.Findings, serviceHealthFindingResponse{
				Code:       finding.Code,
				Summary:    serviceHealthText(finding.Summary),
				DetectedAt: finding.DetectedAt,
			})
		}
		views = append(views, view)
	}

	return views
}

func serviceHealthDeploymentViews(input *serviceHealthInput) []serviceHealthDeploymentResponse {
	views := make([]serviceHealthDeploymentResponse, 0, serviceHealthMaxDeployments)
	if input.latest != nil {
		views = append(views, serviceHealthDeploymentView(input.latest, serviceHealthDeploymentRoleLatest, input.service))
	}
	if input.baseline != nil {
		views = append(views, serviceHealthDeploymentView(
			input.baseline, serviceHealthDeploymentRoleLastSucceeded, input.service,
		))
	}

	return views
}

func serviceHealthDeploymentView(
	deployment *deploymentmodel.Deployment,
	role string,
	service string,
) serviceHealthDeploymentResponse {
	changes := make([]deploymentmodel.Change, 0)
	for _, change := range deployment.Changes {
		if change.ResourceType == "service" && change.ResourceName == service {
			changes = append(changes, change)
		}
	}
	view := serviceHealthDeploymentResponse{
		ID:                 deployment.ID,
		Role:               role,
		Commit:             deployment.Commit,
		Status:             deployment.Status,
		Phase:              deployment.Phase,
		ErrorCode:          deployment.ErrorCode,
		ApplyStatus:        deployment.ApplyStatus,
		VerificationStatus: deployment.VerificationStatus,
		AttemptCount:       deployment.AttemptCount,
		StartedAt:          deployment.StartedAt,
		FinishedAt:         deployment.FinishedAt,
		ErrorDiagnostic:    serviceHealthText(deployment.ErrorDiagnostic),
		ServiceChanged:     len(changes) > 0,
	}
	if len(changes) > serviceHealthMaxServiceChanges {
		changes = changes[:serviceHealthMaxServiceChanges]
		view.ChangesTruncated = true
	}
	view.ServiceChanges = changes

	return view
}

func finalizeServiceHealthFlags(flags []serviceHealthFlag, now time.Time) []serviceHealthFlag {
	result := make([]serviceHealthFlag, 0, len(flags))
	for _, flag := range flags {
		if flag.Since != nil && !flag.Since.IsZero() {
			flag.SinceAgo = serviceHealthAgo(now, *flag.Since)
		} else {
			flag.Since = nil
		}
		result = append(result, flag)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return serviceHealthSeverityRank[result[i].Severity] < serviceHealthSeverityRank[result[j].Severity]
	})

	return result
}

func serviceHealthOverallStatus(flags []serviceHealthFlag, swarmChecked bool) string {
	if !swarmChecked {
		return serviceHealthStatusUnknown
	}
	status := serviceHealthStatusHealthy
	for _, flag := range flags {
		if flag.Severity == serviceHealthSeverityCritical {
			return serviceHealthStatusFailing
		}
		if flag.Severity == serviceHealthSeverityWarning {
			status = serviceHealthStatusDegraded
		}
	}

	return status
}

func hasActiveServiceHealthFlag(flags []serviceHealthFlag, code string) bool {
	for _, flag := range flags {
		if flag.Code == code && flag.Phase == serviceHealthPhaseActive {
			return true
		}
	}

	return false
}

// serviceHealthAgo formats elapsed time compactly because the model cannot reliably compute intervals.
func serviceHealthAgo(now, since time.Time) string {
	elapsed := max(now.Sub(since), 0)
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int64(elapsed/time.Second))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int64(elapsed/time.Minute))
	case elapsed < serviceHealthAgoHoursLimit:
		return fmt.Sprintf("%dh", int64(elapsed/time.Hour))
	default:
		return fmt.Sprintf("%dd", int64(elapsed/serviceHealthDay))
	}
}

// serviceHealthText bounds untrusted text included in the snapshot.
func serviceHealthText(value string) string {
	text, _ := truncateTaskLogText(strings.TrimSpace(value), serviceHealthMaxTextBytes)
	return text
}

func containsFold(value, fragment string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(fragment))
}
