package events

import (
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/compose"
)

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
	}
	if d.Error != nil {
		details["error"] = d.Error.Error()
	}
	return details
}
