package notify

import (
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifications"
)

// StandardTemplate returns the event-owned standard template for an event type.
func StandardTemplate(eventType events.TypeName) (string, bool) {
	switch eventType {
	case events.TypeNameDeploySuccess:
		return eventnotifications.SuccessTemplate(), true
	case events.TypeNameNodeJoined, events.TypeNameNodeConnected, events.TypeNameNodeDisconnected:
		return eventnotifications.DefaultTemplate(), true
	case events.TypeNameServiceMissed, events.TypeNameServicePruned, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted:
		return eventnotifications.DefaultTemplate(), true
	case events.TypeNameServiceCatalogUpdated, events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNetworkCreated:
		return eventnotifications.DefaultTemplate(), true
	case events.TypeNameUserAuthenticated:
		return eventnotifications.DefaultTemplate(), true
	case events.TypeNameAssistantPromptInjectionDetected:
		return eventnotifications.DefaultTemplate(), true
	case events.TypeNameDeployFailed, events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted,
		events.TypeNameAlertOpened, events.TypeNameAlertResolved, events.TypeNameConfigurationChanged,
		events.TypeNameGitPullFailed, events.TypeNameGitPullSucceeded,
		events.TypeNameOutboxDeliveryFailed, events.TypeNameOutboxDeliveryRecovered,
		events.TypeNameOutboxDeliveryDiscarded:
		return "", false
	default:
		return "", false
	}
}

// ReferencePayloads returns representative event-owned payloads used for template validation.
func ReferencePayloads(eventType events.TypeName) ([]any, bool) {
	switch eventType {
	case events.TypeNameDeploySuccess:
		return []any{eventnotifications.DeploymentSuccessReferencePayload()}, true
	case events.TypeNameNodeJoined:
		return []any{eventnotifications.NodeJoinedReferencePayload()}, true
	case events.TypeNameNodeConnected:
		return []any{eventnotifications.NodeConnectedReferencePayload()}, true
	case events.TypeNameNodeDisconnected:
		return []any{eventnotifications.NodeDisconnectedReferencePayload()}, true
	case events.TypeNameServiceMissed, events.TypeNameServicePruned, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted:
		return []any{eventnotifications.ServiceReferencePayload(string(eventType))}, true
	case events.TypeNameServiceCatalogUpdated, events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNetworkCreated:
		return []any{eventnotifications.ReconciliationReferencePayload(string(eventType))}, true
	case events.TypeNameUserAuthenticated:
		return []any{eventnotifications.SecurityReferencePayload()}, true
	case events.TypeNameAssistantPromptInjectionDetected:
		return []any{eventnotifications.AssistantReferencePayload()}, true
	case events.TypeNameDeployFailed, events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted,
		events.TypeNameAlertOpened, events.TypeNameAlertResolved, events.TypeNameConfigurationChanged,
		events.TypeNameGitPullFailed, events.TypeNameGitPullSucceeded,
		events.TypeNameOutboxDeliveryFailed, events.TypeNameOutboxDeliveryRecovered,
		events.TypeNameOutboxDeliveryDiscarded:
		return nil, false
	default:
		return nil, false
	}
}
