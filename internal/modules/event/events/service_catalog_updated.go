package events

// ServiceCatalogUpdated is an internal fact emitted after metadata is committed.
type ServiceCatalogUpdated struct {
	// StackName identifies the refreshed catalog.
	StackName string
}

// Type identifies the internal catalog event.
func (*ServiceCatalogUpdated) Type() Type { return TypeServiceCatalogUpdated }

// Message describes the projection update.
func (*ServiceCatalogUpdated) Message() string { return "Service catalog updated" }

// Details contains non-sensitive identity only.
func (e *ServiceCatalogUpdated) Details() map[string]string {
	return map[string]string{"stack_name": e.StackName}
}
