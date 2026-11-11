package eventnotify

import "github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"

// StandardTemplate returns the standard notification template for an event type.
func StandardTemplate(eventType events.TypeName) (string, bool) {
	switch eventType {
	case events.TypeNameDeploySuccess:
		return SuccessTemplate(), true
	case events.TypeNameNodeJoined, events.TypeNameNodeConnected, events.TypeNameNodeDisconnected:
		return DefaultTemplate(), true
	case events.TypeNameServiceMissed, events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed,
		events.TypeNameServicePruned, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted:
		return DefaultTemplate(), true
	case events.TypeNameServiceCatalogUpdated, events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNetworkCreated:
		return DefaultTemplate(), true
	case events.TypeNameUserAuthenticated:
		return DefaultTemplate(), true
	case events.TypeNameAssistantPromptInjectionDetected:
		return DefaultTemplate(), true
	case events.TypeNameSendNotificationFailed:
		return DefaultTemplate(), true
	case events.TypeNameDeployFailed, events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted,
		events.TypeNameAlertOpened, events.TypeNameAlertResolved, events.TypeNameConfigurationChanged,
		events.TypeNameGitPullFailed, events.TypeNameGitPullSucceeded, events.TypeNameServiceRemovedFromDesired:
		return "", false
	default:
		return "", false
	}
}

// ReferencePayloads returns representative notification payloads used for template validation.
func ReferencePayloads(eventType events.TypeName) ([]any, bool) {
	switch eventType {
	case events.TypeNameDeploySuccess:
		return []any{DeploymentSuccessReferencePayload()}, true
	case events.TypeNameNodeJoined:
		return []any{NodeJoinedReferencePayload()}, true
	case events.TypeNameNodeConnected:
		return []any{NodeConnectedReferencePayload()}, true
	case events.TypeNameNodeDisconnected:
		return []any{NodeDisconnectedReferencePayload()}, true
	case events.TypeNameServiceMissed, events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed,
		events.TypeNameServicePruned, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted:
		return []any{ServiceReferencePayload(string(eventType))}, true
	case events.TypeNameServiceCatalogUpdated, events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNetworkCreated:
		return []any{ReconciliationReferencePayload(string(eventType))}, true
	case events.TypeNameUserAuthenticated:
		return []any{SecurityReferencePayload()}, true
	case events.TypeNameAssistantPromptInjectionDetected:
		return []any{AssistantReferencePayload()}, true
	case events.TypeNameSendNotificationFailed:
		return []any{ReferencePayload()}, true
	case events.TypeNameDeployFailed, events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted,
		events.TypeNameAlertOpened, events.TypeNameAlertResolved, events.TypeNameConfigurationChanged,
		events.TypeNameGitPullFailed, events.TypeNameGitPullSucceeded, events.TypeNameServiceRemovedFromDesired:
		return nil, false
	default:
		return nil, false
	}
}
