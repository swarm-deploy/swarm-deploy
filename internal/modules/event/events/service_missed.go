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
	// Resolved reports that this drift was fixed during the same reconciliation.
	// Older persisted events omit the field and remain unresolved by default.
	Resolved bool
}

func (s *ServiceMissed) Type() Type {
	return TypeServiceMissed
}

func (s *ServiceMissed) Message() string {
	return fmt.Sprintf("Service %s/%s missed", s.StackName, s.ServiceName)
}

func (s *ServiceMissed) Details() map[string]string {
	return map[string]string{
		AttributeStackName:   s.StackName,
		AttributeServiceName: s.ServiceName,
		AttributeCommit:      s.Commit,
	}
}
