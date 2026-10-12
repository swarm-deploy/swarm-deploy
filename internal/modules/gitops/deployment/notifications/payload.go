// Package notifications defines the public notification contract for deployments.
package notifications

import "time"

const (
	// StatusSuccess identifies a completed deployment.
	StatusSuccess = "success"
	// StatusFailed identifies a failed deployment attempt.
	StatusFailed = "failed"
	// StatusPreparationFailed identifies a failure before an attempt was created.
	StatusPreparationFailed = "preparation_failed"
	// StatusInterrupted identifies an attempt with an unknown external outcome.
	StatusInterrupted = "interrupted"
)

// Service is the notification-safe deployment service summary.
type Service struct {
	// Name is the Compose service name.
	Name string `json:"name"`
	// Image is the configured image reference.
	Image string `json:"image"`
}

// Payload is the stable public data contract for deployment templates.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// DeploymentID identifies the persisted deployment attempt when one exists.
	DeploymentID string `json:"deployment_id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// Status is success, failed, preparation_failed, or interrupted.
	Status string `json:"status"`
	// StackName identifies the deployed stack.
	StackName string `json:"stack_name"`
	// Services contains notification-safe service names and images.
	Services []Service `json:"services"`
	// Commit identifies the source revision.
	Commit string `json:"commit"`
	// Error is a safe human-readable failure diagnostic.
	Error string `json:"error"`
	// ErrorCode is the stable failure category.
	ErrorCode string `json:"error_code"`
	// Stage identifies the deployment stage responsible for a failure.
	Stage string `json:"stage"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// SuccessReferencePayload returns representative data used to validate success templates.
func SuccessReferencePayload() Payload {
	return Payload{
		ID: "event-deploy-success", DeploymentID: "deployment-42", Type: "deploySuccess", Status: StatusSuccess,
		StackName: "payments", Commit: "a1b2c3d4", OccurredAt: referenceTime(),
		Services: []Service{{Name: "api", Image: "registry.example.com/payments/api:v2.4.1"}},
	}
}

// FailureReferencePayloads returns representative data for every deployment failure state.
func FailureReferencePayloads() []Payload {
	base := Payload{
		ID: "event-deploy-failed", DeploymentID: "deployment-43", Type: "deployFailed", Status: StatusFailed,
		StackName: "payments", Commit: "d4c3b2a1", Error: "service update rejected",
		ErrorCode: "apply_failed", Stage: "apply", OccurredAt: referenceTime(),
		Services: []Service{{Name: "worker", Image: "registry.example.com/payments/worker:v2.4.2"}},
	}
	preparation := base
	preparation.ID, preparation.DeploymentID = "event-preparation-failed", ""
	preparation.Status, preparation.ErrorCode, preparation.Stage = StatusPreparationFailed, "preparation_failed", "load"
	interrupted := base
	interrupted.ID = "event-deploy-interrupted"
	interrupted.Status, interrupted.ErrorCode, interrupted.Stage = StatusInterrupted, "process_interrupted", "apply"

	return []Payload{base, preparation, interrupted}
}

func referenceTime() time.Time {
	return time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)
}
