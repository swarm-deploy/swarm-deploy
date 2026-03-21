package events

import "github.com/artarts36/swarm-deploy/internal/compose"

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

func (d *DeployFailed) Type() Type {
	return TypeDeployFailed
}
