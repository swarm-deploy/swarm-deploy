package events

import "fmt"

// ServiceMissed is emitted when a service disappeared from cluster runtime.
type ServiceMissed struct {
	// StackName is a stack name where service is expected.
	StackName string
	// ServiceName is a service name inside stack.
	ServiceName string
}

// ServiceRestored is emitted when a missed service has been restored.
type ServiceRestored struct {
	// StackName is a stack name where service was restored.
	StackName string
	// ServiceName is a restored service name inside stack.
	ServiceName string
}

// ServiceRestoreFailed is emitted when automatic restore of missed service fails.
type ServiceRestoreFailed struct {
	// StackName is a stack name where service restoration failed.
	StackName string
	// ServiceName is a target service name inside stack.
	ServiceName string
}

// ServiceReplicasDiverged is emitted when live replicas count differs from desired state.
type ServiceReplicasDiverged struct {
	// StackName is a stack name where replicas drift was detected.
	StackName string
	// ServiceName is a target service name inside stack.
	ServiceName string
}

func (s *ServiceMissed) Type() Type {
	return TypeServiceMissed
}

func (s *ServiceMissed) Message() string {
	return fmt.Sprintf("Service %s/%s disappeared from cluster", s.StackName, s.ServiceName)
}

func (s *ServiceMissed) Details() map[string]string {
	return map[string]string{
		"stack_name":   s.StackName,
		"service_name": s.ServiceName,
	}
}

func (s *ServiceRestored) Type() Type {
	return TypeServiceRestored
}

func (s *ServiceRestored) Message() string {
	return fmt.Sprintf("Service %s/%s restored in cluster", s.StackName, s.ServiceName)
}

func (s *ServiceRestored) Details() map[string]string {
	return map[string]string{
		"stack_name":   s.StackName,
		"service_name": s.ServiceName,
	}
}

func (s *ServiceRestoreFailed) Type() Type {
	return TypeServiceRestoreFailed
}

func (s *ServiceRestoreFailed) Message() string {
	return fmt.Sprintf("Failed to restore service %s/%s", s.StackName, s.ServiceName)
}

func (s *ServiceRestoreFailed) Details() map[string]string {
	return map[string]string{
		"stack_name":   s.StackName,
		"service_name": s.ServiceName,
	}
}

func (s *ServiceReplicasDiverged) Type() Type {
	return TypeServiceReplicasDiverged
}

func (s *ServiceReplicasDiverged) Message() string {
	return fmt.Sprintf("Service %s/%s replicas diverged from desired state", s.StackName, s.ServiceName)
}

func (s *ServiceReplicasDiverged) Details() map[string]string {
	return map[string]string{
		"stack_name":   s.StackName,
		"service_name": s.ServiceName,
	}
}
