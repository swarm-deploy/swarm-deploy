package events

import (
	"fmt"
	"strconv"
)

// ServiceReplicasIncreased is emitted when service replicas count is increased.
type ServiceReplicasIncreased struct {
	// StackName is a stack name where service lives.
	StackName string
	// ServiceName is a stack service name without stack prefix.
	ServiceName string
	// PreviousReplicas is previous desired replicas count.
	PreviousReplicas uint64
	// CurrentReplicas is current desired replicas count after update.
	CurrentReplicas uint64
	// Username is an optional user who triggered the update.
	Username string
}

func (s *ServiceReplicasIncreased) Type() Type {
	return TypeServiceReplicasIncreased
}

func (s *ServiceReplicasIncreased) Message() string {
	return fmt.Sprintf(
		"Service %s/%s replicas increased from %d to %d",
		s.StackName,
		s.ServiceName,
		s.PreviousReplicas,
		s.CurrentReplicas,
	)
}

func (s *ServiceReplicasIncreased) Details() map[string]string {
	details := map[string]string{
		"stack":             s.StackName,
		"service":           s.ServiceName,
		"previous_replicas": strconv.FormatUint(s.PreviousReplicas, 10),
		"current_replicas":  strconv.FormatUint(s.CurrentReplicas, 10),
	}

	if s.Username != "" {
		details["username"] = s.Username
	}

	return details
}

func (s *ServiceReplicasIncreased) WithUsername(username string) Event {
	return &ServiceReplicasIncreased{
		StackName:        s.StackName,
		ServiceName:      s.ServiceName,
		PreviousReplicas: s.PreviousReplicas,
		CurrentReplicas:  s.CurrentReplicas,
		Username:         username,
	}
}

// ServiceReplicasDecreased is emitted when service replicas count is decreased.
type ServiceReplicasDecreased struct {
	// StackName is a stack name where service lives.
	StackName string
	// ServiceName is a stack service name without stack prefix.
	ServiceName string
	// PreviousReplicas is previous desired replicas count.
	PreviousReplicas uint64
	// CurrentReplicas is current desired replicas count after update.
	CurrentReplicas uint64
	// Username is an optional user who triggered the update.
	Username string
}

func (s *ServiceReplicasDecreased) Type() Type {
	return TypeServiceReplicasDecreased
}

func (s *ServiceReplicasDecreased) Message() string {
	return fmt.Sprintf(
		"Service %s/%s replicas decreased from %d to %d",
		s.StackName,
		s.ServiceName,
		s.PreviousReplicas,
		s.CurrentReplicas,
	)
}

func (s *ServiceReplicasDecreased) Details() map[string]string {
	details := map[string]string{
		"stack":             s.StackName,
		"service":           s.ServiceName,
		"previous_replicas": strconv.FormatUint(s.PreviousReplicas, 10),
		"current_replicas":  strconv.FormatUint(s.CurrentReplicas, 10),
	}

	if s.Username != "" {
		details["username"] = s.Username
	}

	return details
}

func (s *ServiceReplicasDecreased) WithUsername(username string) Event {
	return &ServiceReplicasDecreased{
		StackName:        s.StackName,
		ServiceName:      s.ServiceName,
		PreviousReplicas: s.PreviousReplicas,
		CurrentReplicas:  s.CurrentReplicas,
		Username:         username,
	}
}

// ServiceRestarted is emitted when service is restarted by scaling to zero and back.
type ServiceRestarted struct {
	// StackName is a stack name where service lives.
	StackName string
	// ServiceName is a stack service name without stack prefix.
	ServiceName string
	// Username is an optional user who triggered the restart.
	Username string
}

func (s *ServiceRestarted) Type() Type {
	return TypeServiceRestarted
}

func (s *ServiceRestarted) Message() string {
	return fmt.Sprintf("Service %s/%s restarted", s.StackName, s.ServiceName)
}

func (s *ServiceRestarted) Details() map[string]string {
	details := map[string]string{
		"stack":   s.StackName,
		"service": s.ServiceName,
	}

	if s.Username != "" {
		details["username"] = s.Username
	}

	return details
}

func (s *ServiceRestarted) WithUsername(username string) Event {
	return &ServiceRestarted{
		StackName:   s.StackName,
		ServiceName: s.ServiceName,
		Username:    username,
	}
}
