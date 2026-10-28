package events

import "fmt"

// ServiceDrifted is emitted when an existing service differs from desired state.
type ServiceDrifted struct {
	// StackName is the stack containing the drifted service.
	StackName string
	// ServiceName is the service name without stack prefix.
	ServiceName string
	// Commit is the Git revision used for reconciliation.
	Commit string
	// Reason describes the detected difference.
	Reason string
	// Resolved reports that this drift was fixed during the same reconciliation.
	Resolved bool
}

func (s *ServiceDrifted) Type() Type { return TypeServiceDrifted }

func (s *ServiceDrifted) Message() string {
	return fmt.Sprintf("Service %s/%s drifted: %s", s.StackName, s.ServiceName, s.Reason)
}

func (s *ServiceDrifted) Details() map[string]string {
	return map[string]string{
		AttributeStackName:   s.StackName,
		AttributeServiceName: s.ServiceName,
		AttributeCommit:      s.Commit,
		"reason":             s.Reason,
	}
}

// ServiceDriftFixed is emitted after actual Swarm state is confirmed converged.
type ServiceDriftFixed struct {
	// StackName is the stack containing the recovered service.
	StackName string
	// ServiceName is the service name without stack prefix.
	ServiceName string
	// Commit is the Git revision used for reconciliation.
	Commit string
}

func (s *ServiceDriftFixed) Type() Type { return TypeServiceDriftFixed }

func (s *ServiceDriftFixed) Message() string {
	return fmt.Sprintf("Service %s/%s drift fixed", s.StackName, s.ServiceName)
}

func (s *ServiceDriftFixed) Details() map[string]string {
	return map[string]string{
		AttributeStackName:   s.StackName,
		AttributeServiceName: s.ServiceName,
		AttributeCommit:      s.Commit,
	}
}
