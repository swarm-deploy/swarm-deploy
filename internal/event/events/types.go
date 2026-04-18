package events

type Type string

const (
	TypeDeploySuccess                    = "deploySuccess"
	TypeDeployFailed                     = "deployFailed"
	TypeSendNotificationFailed           = "sendNotificationFailed"
	TypeSyncManualStarted                = "syncManualStarted"
	TypeServiceReplicasIncreased         = "serviceReplicasIncreased"
	TypeServiceReplicasDecreased         = "serviceReplicasDecreased"
	TypeUserAuthenticated                = "userAuthenticated"
	TypeAssistantPromptInjectionDetected = "assistantPromptInjectionDetected"
)

type Event interface {
	// Type returns unique event type identifier.
	Type() Type
	// Message returns short human-readable event description.
	Message() string
	// Details returns event-specific details for history and notifications.
	Details() map[string]string
}

var Types = []Type{
	TypeDeploySuccess,
	TypeDeployFailed,
	TypeSendNotificationFailed,
	TypeSyncManualStarted,
	TypeServiceReplicasIncreased,
	TypeServiceReplicasDecreased,
	TypeUserAuthenticated,
	TypeAssistantPromptInjectionDetected,
}
