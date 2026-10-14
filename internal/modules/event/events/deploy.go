package events

import (
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

type DeployEvent struct {
	// DeploymentID references a persisted apply attempt and its safe desired input.
	DeploymentID    string
	StackName       string
	Commit          string
	Services        []compose.Service
	StackDefinition compose.File
}

type DeploySuccess struct {
	DeployEvent
}

type DeployFailed struct {
	DeployEvent

	// Error contains a secret-masked human-readable diagnostic after durable decoding.
	Error error
	// ErrorCode is the stable technical failure category.
	ErrorCode string
	// Stage identifies the pipeline stage that failed.
	Stage string
	Logs  []string
}

// SyncFailed is the user-facing fact produced from an internal synchronization failure.
type SyncFailed struct {
	// DeploymentID identifies a deployment attempt when one was created.
	DeploymentID string
	// StackName identifies the affected stack.
	StackName string
	// Commit identifies the source revision.
	Commit string
	// Services contains notification-safe service names and images.
	Services []compose.Service
	// Status distinguishes failed, preparation_failed, and interrupted synchronization.
	Status string
	// ErrorCode is the stable technical failure category.
	ErrorCode string
	// Stage identifies the synchronization stage that failed.
	Stage string
	// Diagnostic contains bounded, secret-masked technical context.
	Diagnostic string
}

func (d *DeploySuccess) Type() Type {
	return TypeDeploySuccess
}

func (d *DeploySuccess) Message() string {
	return fmt.Sprintf("Deploy succeeded for stack %s", d.StackName)
}

func (d *DeploySuccess) Details() map[string]string {
	return map[string]string{
		AttributeStackName:    d.StackName,
		AttributeCommit:       d.Commit,
		AttributeDeploymentID: d.DeploymentID,
		AttributeStatus:       "success",
	}
}

func (d *DeployFailed) Type() Type {
	return TypeDeployFailed
}

func (d *DeployFailed) Message() string {
	return fmt.Sprintf("Deploy failed for stack %s", d.StackName)
}

func (d *DeployFailed) Details() map[string]string {
	details := map[string]string{
		AttributeStackName:    d.StackName,
		AttributeCommit:       d.Commit,
		AttributeDeploymentID: d.DeploymentID,
		AttributeStatus:       "failed",
		AttributeErrorCode:    d.ErrorCode,
		AttributeStage:        d.Stage,
	}
	if d.Error != nil {
		details[AttributeError] = faults.SafeDiagnostic(d.Error)
	}
	return details
}

// Type returns the public synchronization-failure type.
func (*SyncFailed) Type() Type { return TypeSyncFailed }

// Message returns a user-facing synchronization-failure summary.
func (e *SyncFailed) Message() string {
	return fmt.Sprintf("Synchronization failed for stack %s", e.StackName)
}

// Details returns safe synchronization-failure fields.
func (e *SyncFailed) Details() map[string]string {
	details := map[string]string{
		AttributeStackName: e.StackName, AttributeCommit: e.Commit, AttributeDeploymentID: e.DeploymentID,
		AttributeStatus: e.Status, AttributeErrorCode: e.ErrorCode, AttributeStage: e.Stage,
	}
	if e.Diagnostic != "" {
		details[AttributeError] = faults.SafeDiagnostic(errors.New(e.Diagnostic))
	}
	return details
}
