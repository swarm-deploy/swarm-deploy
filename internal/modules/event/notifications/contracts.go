package notifications

import "time"

const (
	// StatusSuccess identifies a completed deployment.
	StatusSuccess = "success"
)

// DeploymentService is the notification-safe deployment service summary.
type DeploymentService struct {
	// Name is the Compose service name.
	Name string `json:"name"`
	// Image is the configured image reference.
	Image string `json:"image"`
}

// DeploymentPayload is the stable public data contract for deployment templates.
type DeploymentPayload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// DeploymentID identifies the persisted desired state when one exists.
	DeploymentID string `json:"deployment_id"`
	// AttemptID identifies the execution which produced the notification.
	AttemptID string `json:"attempt_id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// Status identifies a successful deployment.
	Status string `json:"status"`
	// StackName identifies the deployed stack.
	StackName string `json:"stack_name"`
	// Services contains notification-safe service names and images.
	Services []DeploymentService `json:"services"`
	// Commit identifies the source revision.
	Commit string `json:"commit"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// NodePayload is the stable public data contract for node notification templates.
type NodePayload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// NodeID is the Docker Swarm node identifier.
	NodeID string `json:"node_id"`
	// NodeName is the Docker Swarm node hostname.
	NodeName string `json:"node_name"`
	// Role is the node role when available.
	Role string `json:"role"`
	// Status is the observed node status when available.
	Status string `json:"status"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ServicePayload is the stable public data contract for service notification templates.
type ServicePayload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// StackName identifies the stack containing the service.
	StackName string `json:"stack_name"`
	// ServiceName identifies the service without a stack prefix.
	ServiceName string `json:"service_name"`
	// Commit identifies the source revision when available.
	Commit string `json:"commit"`
	// Username identifies the user who initiated the action when available.
	Username string `json:"username"`
	// PreviousReplicas is the former desired replica count.
	PreviousReplicas uint64 `json:"previous_replicas"`
	// CurrentReplicas is the new desired replica count.
	CurrentReplicas uint64 `json:"current_replicas"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ReconciliationPayload is the stable public data contract for reconciliation notification templates.
type ReconciliationPayload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// StackName identifies an affected stack when available.
	StackName string `json:"stack_name"`
	// Username identifies the user who started a manual sync.
	Username string `json:"username"`
	// Queued reports whether a webhook scheduled reconciliation.
	Queued bool `json:"queued"`
	// NetworkName identifies a created network.
	NetworkName string `json:"network_name"`
	// NetworkID identifies a created Docker Swarm network.
	NetworkID string `json:"network_id"`
	// Driver is the created network driver.
	Driver string `json:"driver"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// SecurityPayload is the stable public data contract for security notification templates.
type SecurityPayload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// Username identifies the authenticated user when available.
	Username string `json:"username"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// AssistantPayload is the stable public data contract for assistant notification templates.
type AssistantPayload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// Detector identifies the prompt-injection detector.
	Detector string `json:"detector"`
	// Username identifies the user when available.
	Username string `json:"username"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// DeploymentSuccessReferencePayload returns representative data used to validate success templates.
func DeploymentSuccessReferencePayload() DeploymentPayload {
	return DeploymentPayload{
		ID: "event-deploy-success", DeploymentID: "deployment-42", Type: "DeploySuccess", Status: StatusSuccess,
		StackName: "payments", Commit: "a1b2c3d4", OccurredAt: referenceTime(),
		Services: []DeploymentService{{Name: "api", Image: "registry.example.com/payments/api:v2.4.1"}},
	}
}

// NodeJoinedReferencePayload returns representative node-joined data.
func NodeJoinedReferencePayload() NodePayload {
	return NodePayload{ID: "event-node-joined", Type: "NodeJoined", NodeID: "node-1", NodeName: "worker-1",
		Role: "worker", Message: "Node worker-1 joined the swarm", OccurredAt: referenceTime()}
}

// NodeConnectedReferencePayload returns representative node-connected data.
func NodeConnectedReferencePayload() NodePayload {
	return NodePayload{ID: "event-node-connected", Type: "NodeConnected", NodeID: "node-1", NodeName: "worker-1",
		Status: "ready", Message: "Node worker-1 connected", OccurredAt: referenceTime()}
}

// NodeDisconnectedReferencePayload returns representative node-disconnected data.
func NodeDisconnectedReferencePayload() NodePayload {
	return NodePayload{ID: "event-node-disconnected", Type: "NodeDisconnected", NodeID: "node-1", NodeName: "worker-1",
		Status: "down", Message: "Node worker-1 disconnected", OccurredAt: referenceTime()}
}

// ServiceReferencePayload returns representative data for a service event type.
func ServiceReferencePayload(eventType string) ServicePayload {
	const (
		referencePreviousReplicas = 2
		referenceCurrentReplicas  = 3
	)

	payload := ServicePayload{ID: "event-service", Type: eventType, StackName: "payments", ServiceName: "api",
		Commit: "a1b2c3d4", Username: "operator", PreviousReplicas: referencePreviousReplicas,
		CurrentReplicas: referenceCurrentReplicas, OccurredAt: referenceTime()}
	switch eventType {
	case "ServiceMissed":
		payload.Message = "Service payments/api missed"
	case "ServicePruned":
		payload.Message = "Service payments/api pruned"
	case "ServiceReplicasIncreased":
		payload.Message = "Service payments/api replicas increased from 2 to 3"
	case "ServiceReplicasDecreased":
		payload.PreviousReplicas, payload.CurrentReplicas = referenceCurrentReplicas, referencePreviousReplicas
		payload.Message = "Service payments/api replicas decreased from 3 to 2"
	case "ServiceRestarted":
		payload.Message = "Service payments/api restarted"
	default:
		payload.Message = "Service event"
	}
	return payload
}

// ReconciliationReferencePayload returns representative data for a reconciliation event type.
func ReconciliationReferencePayload(eventType string) ReconciliationPayload {
	payload := ReconciliationPayload{ID: "event-reconciliation", Type: eventType, StackName: "payments",
		Username: "operator", Queued: true, NetworkName: "payments", NetworkID: "network-42", Driver: "overlay",
		OccurredAt: referenceTime()}
	switch eventType {
	case "ServiceCatalogUpdated":
		payload.Message = "Service catalog updated"
	case "SyncManualStarted":
		payload.Message = "Manual sync started"
	case "WebhookReceived":
		payload.Message = "Webhook received"
	case "NetworkCreated":
		payload.Message = "Network payments created"
	default:
		payload.Message = "Reconciliation event"
	}
	return payload
}

// SecurityReferencePayload returns representative authenticated-user data.
func SecurityReferencePayload() SecurityPayload {
	return SecurityPayload{ID: "event-user-authenticated", Type: "UserAuthenticated", Username: "operator",
		Message: "User operator authenticated", OccurredAt: referenceTime()}
}

// AssistantReferencePayload returns representative prompt-injection data.
func AssistantReferencePayload() AssistantPayload {
	return AssistantPayload{ID: "event-prompt-injection", Type: "AssistantPromptInjectionDetected", Detector: "regexp",
		Username: "operator", Message: "Detected prompt injection", OccurredAt: referenceTime()}
}

func referenceTime() time.Time {
	return time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)
}
