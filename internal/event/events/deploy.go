package events

import (
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// DeployDenied is emitted when an image policy prevents a service deployment.
type DeployDenied struct {
	// StackName is the stack that contains the denied service.
	StackName string
	// ServiceName is the service that was denied.
	ServiceName string
	// Image is the denied image reference.
	Image string
	// Policy is the violated policy name.
	Policy string
}

func (d *DeployDenied) Type() Type {
	return TypeDeployDenied
}

func (d *DeployDenied) Message() string {
	return fmt.Sprintf("Deploy denied for service %s/%s", d.StackName, d.ServiceName)
}

func (d *DeployDenied) Details() map[string]string {
	return map[string]string{
		"stack_name":   d.StackName,
		"service_name": d.ServiceName,
		"image":        d.Image,
		"policy":       d.Policy,
	}
}

type DeploySuccess struct {
	StackName string
	Commit    string
	Services  []compose.Service
}

type DeployFailed struct {
	StackName string
	Commit    string
	Services  []compose.Service
	Error     error
	Logs      []string
}

func (d *DeploySuccess) Type() Type {
	return TypeDeploySuccess
}

func (d *DeploySuccess) Message() string {
	return fmt.Sprintf("Deploy succeeded for stack %s", d.StackName)
}

func (d *DeploySuccess) Details() map[string]string {
	return map[string]string{
		"stack":  d.StackName,
		"commit": d.Commit,
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
		"stack":  d.StackName,
		"commit": d.Commit,
		"logs":   strings.Join(d.Logs, "\n"),
	}
	if d.Error != nil {
		details["error"] = d.Error.Error()
	}
	return details
}
