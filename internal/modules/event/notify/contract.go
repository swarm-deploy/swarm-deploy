package notify

import (
	assistantnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/notifications"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifications"
	controllernotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/notifications"
	deploymentnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment/notifications"
	nodenotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node/notifications"
	servicenotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/notifications"
	securitynotifications "github.com/swarm-deploy/swarm-deploy/internal/security/notifications"
)

// StandardTemplate returns the source-owned standard template for an event type.
func StandardTemplate(eventType events.TypeName) (string, bool) {
	switch eventType {
	case events.TypeNameDeploySuccess:
		return deploymentnotifications.SuccessTemplate(), true
	case events.TypeNameDeployFailed:
		return deploymentnotifications.FailedTemplate(), true
	case events.TypeNameNodeJoined, events.TypeNameNodeConnected, events.TypeNameNodeDisconnected:
		return nodenotifications.DefaultTemplate(), true
	case events.TypeNameServiceMissed, events.TypeNameServicePruned, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted:
		return servicenotifications.DefaultTemplate(), true
	case events.TypeNameServiceCatalogUpdated, events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNetworkCreated:
		return controllernotifications.DefaultTemplate(), true
	case events.TypeNameUserAuthenticated:
		return securitynotifications.DefaultTemplate(), true
	case events.TypeNameAssistantPromptInjectionDetected:
		return assistantnotifications.DefaultTemplate(), true
	case events.TypeNameSendNotificationFailed:
		return eventnotifications.DefaultTemplate(), true
	case events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted:
		return "", false
	default:
		return "", false
	}
}

// ReferencePayloads returns representative source-owned payloads used for template validation.
func ReferencePayloads(eventType events.TypeName) ([]any, bool) {
	switch eventType {
	case events.TypeNameDeploySuccess:
		return []any{deploymentnotifications.SuccessReferencePayload()}, true
	case events.TypeNameDeployFailed:
		payloads := deploymentnotifications.FailureReferencePayloads()
		result := make([]any, 0, len(payloads))
		for _, payload := range payloads {
			result = append(result, payload)
		}
		return result, true
	case events.TypeNameNodeJoined:
		return []any{nodenotifications.JoinedReferencePayload()}, true
	case events.TypeNameNodeConnected:
		return []any{nodenotifications.ConnectedReferencePayload()}, true
	case events.TypeNameNodeDisconnected:
		return []any{nodenotifications.DisconnectedReferencePayload()}, true
	case events.TypeNameServiceMissed, events.TypeNameServicePruned, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted:
		return []any{servicenotifications.ReferencePayload(string(eventType))}, true
	case events.TypeNameServiceCatalogUpdated, events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNetworkCreated:
		return []any{controllernotifications.ReferencePayload(string(eventType))}, true
	case events.TypeNameUserAuthenticated:
		return []any{securitynotifications.ReferencePayload()}, true
	case events.TypeNameAssistantPromptInjectionDetected:
		return []any{assistantnotifications.ReferencePayload()}, true
	case events.TypeNameSendNotificationFailed:
		return []any{eventnotifications.ReferencePayload()}, true
	case events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted:
		return nil, false
	default:
		return nil, false
	}
}
