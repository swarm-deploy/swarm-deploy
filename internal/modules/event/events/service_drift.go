package events

import "fmt"

type serviceDriftEvent struct {
	stackName   string
	serviceName string
	commit      string
}

func (s serviceDriftEvent) details() map[string]string {
	return map[string]string{
		AttributeStackName:   s.stackName,
		AttributeServiceName: s.serviceName,
		AttributeCommit:      s.commit,
	}
}

// ServiceRestored is emitted after self-heal restores a missing service.
type ServiceRestored struct {
	// StackName is the stack containing the restored service.
	StackName string
	// ServiceName is the service name without stack prefix.
	ServiceName string
	// Commit is the Git revision used for reconciliation.
	Commit string
}

func (s *ServiceRestored) Type() Type { return TypeServiceRestored }

func (s *ServiceRestored) Message() string {
	return fmt.Sprintf("Service %s/%s restored", s.StackName, s.ServiceName)
}

func (s *ServiceRestored) Details() map[string]string {
	return serviceDriftEvent{s.StackName, s.ServiceName, s.Commit}.details()
}

// ServiceRestoreFailed is emitted when self-heal cannot restore a missing service.
type ServiceRestoreFailed struct {
	// StackName is the stack containing the missing service.
	StackName string
	// ServiceName is the service name without stack prefix.
	ServiceName string
	// Commit is the Git revision used for reconciliation.
	Commit string
}

func (s *ServiceRestoreFailed) Type() Type { return TypeServiceRestoreFailed }

func (s *ServiceRestoreFailed) Message() string {
	return fmt.Sprintf("Failed to restore service %s/%s", s.StackName, s.ServiceName)
}

func (s *ServiceRestoreFailed) Details() map[string]string {
	return serviceDriftEvent{s.StackName, s.ServiceName, s.Commit}.details()
}

// ServiceReplicasDiverged is emitted when live and desired replicas differ.
type ServiceReplicasDiverged struct {
	// StackName is the stack containing the service.
	StackName string
	// ServiceName is the service name without stack prefix.
	ServiceName string
	// Commit is the Git revision used for reconciliation.
	Commit string
}

func (s *ServiceReplicasDiverged) Type() Type { return TypeServiceReplicasDiverged }

func (s *ServiceReplicasDiverged) Message() string {
	return fmt.Sprintf("Service %s/%s replicas diverged from desired state", s.StackName, s.ServiceName)
}

func (s *ServiceReplicasDiverged) Details() map[string]string {
	return serviceDriftEvent{s.StackName, s.ServiceName, s.Commit}.details()
}
