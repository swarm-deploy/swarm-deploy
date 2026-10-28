package events

// ServiceRemovedFromDesired is emitted when an accepted desired state no longer contains a service.
type ServiceRemovedFromDesired struct {
	// StackName identifies the stack whose desired state changed.
	StackName string
	// ServiceName identifies the removed desired service.
	ServiceName string
	// Commit identifies the accepted source revision.
	Commit string
}

// Type returns the internal service removal event type.
func (*ServiceRemovedFromDesired) Type() Type { return TypeServiceRemovedFromDesired }

// Message returns a concise description of the desired-state transition.
func (*ServiceRemovedFromDesired) Message() string { return "Service removed from desired state" }

// Details returns safe service identity attributes.
func (s *ServiceRemovedFromDesired) Details() map[string]string {
	return map[string]string{
		AttributeStackName:   s.StackName,
		AttributeServiceName: s.ServiceName,
		AttributeCommit:      s.Commit,
	}
}
