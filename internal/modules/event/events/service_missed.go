package events

import "fmt"

// ServiceMissed is emitted when a desired service is absent in live swarm state.
type ServiceMissed struct {
	// StackName is a stack name where service is expected.
	StackName string
	// ServiceName is an expected service name without stack prefix.
	ServiceName string
	// Commit is a git revision used for current sync run.
	Commit string
}

func (s *ServiceMissed) Type() Type {
	return TypeServiceMissed
}

func (s *ServiceMissed) Message() string {
	return fmt.Sprintf("Service %s/%s missed", s.StackName, s.ServiceName)
}

func (s *ServiceMissed) Details() map[string]string {
	return map[string]string{
		"stack_name":   s.StackName,
		"service_name": s.ServiceName,
		"commit":       s.Commit,
	}
}
