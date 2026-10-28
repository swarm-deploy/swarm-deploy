// Package model owns desired-state deployments, their attempts and successful baselines.
package model

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// Status describes a deployment or attempt outcome, not Swarm service health.
type Status string

const (
	// Running is committed before any external apply action.
	Running Status = "running"
	// Succeeded means apply returned successfully; it does not promise service convergence.
	Succeeded Status = "succeeded"
	// Failed means an apply action failed.
	Failed Status = "failed"
	// Interrupted means the process stopped before recording an apply outcome.
	Interrupted Status = "interrupted"
	// Superseded means a newer effective desired state replaced this unfinished deployment.
	Superseded Status = "superseded"
)

// ComparisonStatus describes whether the actual pre-deployment Swarm state is known.
type ComparisonStatus string

const (
	// ComparisonKnown is reserved for a verified comparison with live state.
	ComparisonKnown ComparisonStatus = "known"
	// ComparisonUnknown means live convergence has not been verified.
	ComparisonUnknown ComparisonStatus = "unknown"
)

// ComparisonBasis identifies the snapshot used to produce the displayed diff.
type ComparisonBasis string

const (
	// BasisNone means no trustworthy previous snapshot exists.
	BasisNone ComparisonBasis = "none"
	// BasisSuccessfulBaseline compares with the last successfully committed desired snapshot.
	BasisSuccessfulBaseline ComparisonBasis = "successful_baseline"
	// BasisLastAttempt compares with the desired snapshot of the latest uncertain attempt.
	BasisLastAttempt ComparisonBasis = "last_attempt"
	// BasisObservedState is reserved for a snapshot reconstructed from live Swarm state.
	BasisObservedState ComparisonBasis = "observed_state"
)

// Phase identifies the deployment stage responsible for its current outcome.
type Phase string

const (
	// PhaseApply covers the Docker stack apply operation.
	PhaseApply Phase = "apply"
	// PhaseVerification covers reading live state after apply.
	PhaseVerification Phase = "verification"
	// PhaseCleanup covers pruning resources after successful apply and observation.
	PhaseCleanup Phase = "cleanup"
	// PhaseCompleted means every tracked stage completed successfully.
	PhaseCompleted Phase = "completed"
)

// StageStatus describes one independently reported deployment stage.
type StageStatus string

const (
	// StagePending has not started yet.
	StagePending StageStatus = "pending"
	// StageRunning is currently executing.
	StageRunning StageStatus = "running"
	// StageSucceeded completed successfully.
	StageSucceeded StageStatus = "succeeded"
	// StageFailed completed with a known failure.
	StageFailed StageStatus = "failed"
	// StageUnknown has an indeterminate external outcome.
	StageUnknown StageStatus = "unknown"
	// StageSkipped did not run because an earlier stage stopped the attempt.
	StageSkipped StageStatus = "skipped"
)

// ActualStateStatus describes whether live Swarm state was observed after apply.
type ActualStateStatus string

const (
	// ActualStateObserved means live state was read successfully.
	ActualStateObserved ActualStateStatus = "observed"
	// ActualStateUnknown means the post-attempt Swarm state is not known.
	ActualStateUnknown ActualStateStatus = "unknown"
)

// ChangeOperation describes a field transition.
type ChangeOperation string

const (
	// OperationAdded means the field did not exist in the basis.
	OperationAdded ChangeOperation = "added"
	// OperationChanged means the field existed with a different value.
	OperationChanged ChangeOperation = "changed"
	// OperationRemoved means the field no longer exists.
	OperationRemoved ChangeOperation = "removed"
)

// Change describes a semantic value transition without exposing secrets.
type Change struct {
	// ResourceType is a stable public resource kind such as service, config or secret.
	ResourceType string `json:"resourceType"`
	// ResourceName identifies the affected resource without exposing Go model paths.
	ResourceName string `json:"resourceName"`
	// Field is a stable dotted field name such as environment.MODE.
	Field string `json:"field"`
	// Operation describes whether the field was added, changed or removed.
	Operation ChangeOperation `json:"operation"`
	// Before is the old safe value, absent when newly added.
	Before *string `json:"before,omitempty"`
	// After is the new safe value, absent when removed.
	After *string `json:"after,omitempty"`
	// Redacted indicates that a sensitive value changed without disclosing it.
	Redacted bool `json:"redacted,omitempty"`
}

// ChangeSummary contains field-level transition totals.
type ChangeSummary struct {
	// Added is the number of added fields.
	Added int `json:"added"`
	// Changed is the number of changed fields.
	Changed int `json:"changed"`
	// Removed is the number of removed fields.
	Removed int `json:"removed"`
	// Redacted is the number of transitions whose values are hidden.
	Redacted int `json:"redacted"`
}

// ResourceSummary contains distinct affected resource totals.
type ResourceSummary struct {
	// Services is the number of affected services.
	Services int `json:"services"`
	// Configs is the number of affected configs.
	Configs int `json:"configs"`
	// Secrets is the number of affected secrets.
	Secrets int `json:"secrets"`
	// Networks is the number of affected networks.
	Networks int `json:"networks"`
	// Volumes is the number of affected volumes.
	Volumes int `json:"volumes"`
}

// InitJobStatus describes the outcome of one Init Job execution.
type InitJobStatus string

const (
	// InitJobSucceeded means the Docker task completed successfully.
	InitJobSucceeded InitJobStatus = "succeeded"
	// InitJobFailed means the Docker task completed with a known failure.
	InitJobFailed InitJobStatus = "failed"
	// InitJobInterrupted means execution ended before a complete outcome was observed.
	InitJobInterrupted InitJobStatus = "interrupted"
)

// InitJobExecution is one completed Init Job execution within a Deployment Attempt.
type InitJobExecution struct {
	// AttemptID identifies the parent Deployment Attempt.
	AttemptID string `json:"attempt_id"`
	// Ordinal is the one-based execution order within the Attempt.
	Ordinal int `json:"ordinal"`
	// ServiceName identifies the service declaring the Init Job.
	ServiceName string `json:"service_name"`
	// JobName identifies the Init Job within the service.
	JobName string `json:"job_name"`
	// Status is the observed execution outcome.
	Status InitJobStatus `json:"status"`
	// StartedAt is recorded immediately before Docker service creation.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is recorded when execution stops, before log collection.
	FinishedAt time.Time `json:"finished_at"`
	// Logs contains original Docker output for persistence and is never serialized automatically.
	Logs []byte `json:"-"`
	// LogsAvailable distinguishes unavailable logs from successfully retrieved empty output.
	LogsAvailable bool `json:"logs_available"`
	// LogsTruncated reports that only the last bounded portion of output was retained.
	LogsTruncated bool `json:"logs_truncated"`
}

// Attempt is one execution of the deployment pipeline for a Deployment.
type Attempt struct {
	// ID uniquely identifies this execution.
	ID string `json:"id"`
	// DeploymentID identifies the immutable desired state being executed.
	DeploymentID string `json:"deployment_id"`
	// Stack is populated for lifecycle inspection and is not persisted on the attempt row.
	Stack string `json:"-"`
	// Number is the monotonically increasing attempt number within the deployment.
	Number int `json:"number"`
	// Status is the durable execution outcome.
	Status Status `json:"status"`
	// StartedAt is recorded before external apply.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is absent while running.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// ErrorCode is a safe diagnostic category.
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorDiagnostic is bounded and secret-masked before persistence.
	ErrorDiagnostic string `json:"error_diagnostic,omitempty"`
	// Phase identifies the stage responsible for the current outcome.
	Phase Phase `json:"phase"`
	// ApplyStatus reports Docker apply independently from later stages.
	ApplyStatus StageStatus `json:"apply_status"`
	// VerificationStatus reports explicit convergence verification.
	VerificationStatus StageStatus `json:"verification_status"`
	// CleanupStatus reports post-apply pruning.
	CleanupStatus StageStatus `json:"cleanup_status"`
	// ActualStateStatus reports whether post-attempt Swarm state was observed.
	ActualStateStatus ActualStateStatus `json:"actual_state_status"`
	// ObservedAt is set when live Swarm state was read successfully.
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	// InitJobs contains execution metadata in original execution order; raw logs are loaded separately.
	InitJobs []InitJobExecution `json:"init_jobs,omitempty"`
}

// Deployment is one immutable effective desired state with zero or more persisted execution attempts.
type Deployment struct {
	// ID uniquely identifies this desired state; retries reuse it.
	ID string `json:"id"`
	// Stack identifies the deployment target.
	Stack string `json:"stack"`
	// Commit identifies the Git revision being applied.
	Commit string `json:"commit"`
	// Status is derived from the latest attempt, unless superseded.
	Status Status `json:"status"`
	// StartedAt is recorded before external apply.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is absent while running.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// ErrorCode is a safe diagnostic category, never raw errors or logs.
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorDiagnostic is bounded and secret-masked before persistence.
	ErrorDiagnostic string `json:"error_diagnostic,omitempty"`
	// Phase identifies the stage responsible for the current outcome.
	Phase Phase `json:"phase"`
	// ApplyStatus reports Docker apply independently from later stages.
	ApplyStatus StageStatus `json:"apply_status"`
	// VerificationStatus reports explicit convergence verification (currently skipped).
	VerificationStatus StageStatus `json:"verification_status"`
	// CleanupStatus reports post-apply pruning.
	CleanupStatus StageStatus `json:"cleanup_status"`
	// ActualStateStatus reports whether post-attempt Swarm state was observed.
	ActualStateStatus ActualStateStatus `json:"actual_state_status"`
	// ObservedAt is set when live Swarm state was read successfully.
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	// ComparisonBasis identifies the snapshot used to calculate Changes.
	ComparisonBasis ComparisonBasis `json:"comparison_basis"`
	// ComparisonStatus reports whether live state has been verified against desired.
	ComparisonStatus ComparisonStatus `json:"comparison_status"`
	// BasisDeploymentID identifies the attempt that supplied the basis snapshot.
	BasisDeploymentID string `json:"basis_deployment_id,omitempty"`
	// Summary contains field transition totals for lightweight list queries.
	Summary ChangeSummary `json:"summary"`
	// Resources contains distinct affected resource totals.
	Resources ResourceSummary `json:"resources"`
	// Changes are relative to ComparisonBasis, not necessarily actual Swarm state.
	Changes []Change `json:"changes"`
	// DiffAvailable distinguishes a verified zero diff from missing comparison data.
	DiffAvailable bool `json:"diff_available"`
	// LatestAttemptID identifies the latest persisted attempt; legacy deployments may have none.
	LatestAttemptID string `json:"latest_attempt_id"`
	// AttemptCount is the number of persisted attempts for this desired state.
	AttemptCount int `json:"attempt_count"`
	// Attempts contains immutable execution history in newest-first order.
	Attempts []Attempt `json:"attempts,omitempty"`
}

// DeploymentSummary is the lightweight list representation of a desired-state deployment.
type DeploymentSummary struct {
	// ID uniquely identifies this attempt.
	ID string
	// Stack identifies the deployment target.
	Stack string
	// Commit identifies the Git revision being applied.
	Commit string
	// Status is the durable overall attempt outcome.
	Status Status
	// StartedAt is recorded before external apply.
	StartedAt time.Time
	// FinishedAt is absent while running.
	FinishedAt *time.Time
	// ErrorCode is a safe diagnostic category.
	ErrorCode string
	// Phase identifies the current or failing stage.
	Phase Phase
	// ApplyStatus reports Docker apply independently from later stages.
	ApplyStatus StageStatus
	// VerificationStatus reports explicit convergence verification.
	VerificationStatus StageStatus
	// CleanupStatus reports pruning.
	CleanupStatus StageStatus
	// ActualStateStatus reports whether live Swarm state was observed.
	ActualStateStatus ActualStateStatus
	// ObservedAt is set after successful live-state observation.
	ObservedAt *time.Time
	// ComparisonBasis identifies the diff basis snapshot.
	ComparisonBasis ComparisonBasis
	// ComparisonStatus reports whether live state has been verified against desired.
	ComparisonStatus ComparisonStatus
	// BasisDeploymentID identifies the successful deployment supplying the basis.
	BasisDeploymentID string
	// Summary contains field transition totals.
	Summary ChangeSummary
	// Resources contains distinct affected resource totals.
	Resources ResourceSummary
	// DiffAvailable distinguishes a verified zero diff from unavailable comparison data.
	DiffAvailable bool
	// LatestAttemptID identifies the latest persisted attempt; legacy deployments may have none.
	LatestAttemptID string
	// AttemptCount is the number of persisted attempts for this desired state.
	AttemptCount int
}

// ServiceDeployment contains the fields needed to display a deployment for one service.
type ServiceDeployment struct {
	// ID uniquely identifies the desired-state deployment.
	ID string
	// Commit identifies the Git revision being applied.
	Commit string
	// Status is the deployment outcome projected from its latest attempt.
	Status Status
	// StartedAt is recorded before external apply.
	StartedAt time.Time
	// Image is the service image in the deployment's desired snapshot.
	Image string
}

// Page is a stable cursor page of lightweight deployment summaries.
type Page struct {
	// Deployments contains desired-state deployments in newest-first order.
	Deployments []DeploymentSummary
	// NextCursor continues after the last returned deployment, when more exist.
	NextCursor string
}

// Prepared contains the redacted effective Compose and private comparison fingerprints.
// It is persisted in the deployment repository, never in an event envelope.
type Prepared struct {
	// Definition retains supported effective Compose values after masking.
	Definition compose.File `json:"definition"`
	// Digest identifies the unmasked effective state without retaining plaintext.
	Digest string `json:"digest"`
	// Fields contains safe display values indexed by semantic path.
	Fields map[string]string `json:"fields"`
	// Fingerprints distinguish changes to values which have identical masked displays.
	Fingerprints map[string]string `json:"fingerprints"`
	// Redacted marks paths whose values have been masked.
	Redacted map[string]bool `json:"redacted"`
}

// ListFilter selects recent desired-state deployments.
type ListFilter struct {
	// Stack restricts results to a stack; empty means all stacks.
	Stack string
	// Service restricts results to deployments containing a service in their desired configuration.
	Service string
	// Status restricts results to one deployment outcome; empty means all statuses.
	Status Status
	// ErrorCode restricts results to one stable failure category; empty means all categories.
	ErrorCode string
	// Phase restricts results to one lifecycle phase; empty means all phases.
	Phase Phase
	// Limit bounds returned deployments; values outside 1..100 use 20.
	Limit int
	// Cursor continues a previous newest-first page.
	Cursor string
}
