package events

import (
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
