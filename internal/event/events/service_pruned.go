package events

import "fmt"

// ServicePruned is emitted when orphaned managed service is removed during sync.
type ServicePruned struct {
	// StackName is a stack name where service lived.
	StackName string
	// ServiceName is a removed service name without stack prefix.
	ServiceName string
	// Commit is a git revision used for current sync run.
	Commit string
}

func (s *ServicePruned) Type() Type {
	return TypeServicePruned
}

func (s *ServicePruned) Message() string {
	return fmt.Sprintf("Service %s/%s pruned", s.StackName, s.ServiceName)
}

func (s *ServicePruned) Details() map[string]string {
	return map[string]string{
		"stack_name":   s.StackName,
		"service_name": s.ServiceName,
		"commit":       s.Commit,
	}
}
