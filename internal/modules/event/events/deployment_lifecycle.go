package events

import (
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// DeployPreparationFailed reports a safe pre-apply failure without inventing a deployment attempt.
type DeployPreparationFailed struct {
	// StackName identifies the stack whose desired input could not be prepared.
	StackName string
	// Commit identifies the source revision.
	Commit string
	// Services contains the same notification-safe service summary as DeployFailed.
	Services []compose.Service
	// Error contains a secret-masked human-readable diagnostic after durable decoding.
	Error error
	// ErrorCode is a stable safe category and never contains raw error text.
	ErrorCode string
	// Stage identifies the preparation stage that failed.
	Stage string
}

// Type returns the durable preparation-failure event type.
func (*DeployPreparationFailed) Type() Type { return TypeDeployPreparationFailed }

// Message returns a secret-safe notification message.
func (e *DeployPreparationFailed) Message() string {
	return fmt.Sprintf("Deployment preparation failed for stack %s", e.StackName)
}

// Details returns only stable categories and identifiers.
func (e *DeployPreparationFailed) Details() map[string]string {
	details := map[string]string{"stack": e.StackName, "commit": e.Commit, "error": e.ErrorCode, "stage": e.Stage}
	if e.Error != nil {
		details["diagnostic"] = e.Error.Error()
	}
	return details
}

// DeployInterrupted reports an apply whose external outcome is unknown.
type DeployInterrupted struct {
	// DeploymentID identifies the interrupted attempt.
	DeploymentID string
	// StackName identifies the affected stack.
	StackName string
	// Commit identifies the source revision.
	Commit string
	// Services contains the same notification-safe service summary as DeployFailed.
	Services []compose.Service
	// Error is populated with a generic safe diagnostic after durable decoding.
	Error error
	// Reason is a stable safe category for the unknown outcome.
	Reason string
	// Stage identifies the pipeline stage whose outcome is unknown.
	Stage string
}

// Type returns the durable interrupted-attempt event type.
func (*DeployInterrupted) Type() Type { return TypeDeployInterrupted }

// Message returns a secret-safe notification message.
func (e *DeployInterrupted) Message() string {
	return fmt.Sprintf("Deployment interrupted for stack %s", e.StackName)
}

// Details returns only stable categories and identifiers.
func (e *DeployInterrupted) Details() map[string]string {
	return map[string]string{"stack": e.StackName, "commit": e.Commit, "error": e.Reason, "stage": e.Stage}
}
