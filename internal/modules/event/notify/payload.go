package notify

import (
	"fmt"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifications"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func notificationPayload(envelope events.Envelope) (any, error) {
	occurredAt := envelope.OccurredAt.UTC()
	eventID := envelope.ID
	switch event := envelope.Event.(type) {
	case *events.DeploySuccess:
		return deploymentPayload(eventID, occurredAt, events.TypeNameDeploySuccess, eventnotifications.StatusSuccess,
			event.DeploymentID, event.AttemptID, event.StackName, event.Commit, event.Services), nil
	case *events.NodeJoined:
		return eventnotifications.NodePayload{ID: eventID, Type: event.Type().String(), NodeID: event.NodeID,
			NodeName: event.NodeName, Role: event.Role, Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.NodeConnected:
		return eventnotifications.NodePayload{ID: eventID, Type: event.Type().String(), NodeID: event.NodeID,
			NodeName: event.NodeName, Status: event.Status, Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.NodeDisconnected:
		return eventnotifications.NodePayload{ID: eventID, Type: event.Type().String(), NodeID: event.NodeID,
			NodeName: event.NodeName, Status: event.Status, Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.ServiceMissed:
		return servicePayload(eventID, occurredAt, event.Type().String(), event.StackName, event.ServiceName,
			event.Commit, "", 0, 0, event.Message()), nil
	case *events.ServicePruned:
		return servicePayload(eventID, occurredAt, event.Type().String(), event.StackName, event.ServiceName,
			event.Commit, "", 0, 0, event.Message()), nil
	case *events.ServiceReplicasIncreased:
		return servicePayload(eventID, occurredAt, event.Type().String(), event.StackName, event.ServiceName,
			"", event.Username, event.PreviousReplicas, event.CurrentReplicas, event.Message()), nil
	case *events.ServiceReplicasDecreased:
		return servicePayload(eventID, occurredAt, event.Type().String(), event.StackName, event.ServiceName,
			"", event.Username, event.PreviousReplicas, event.CurrentReplicas, event.Message()), nil
	case *events.ServiceRestarted:
		return servicePayload(eventID, occurredAt, event.Type().String(), event.StackName, event.ServiceName,
			"", event.Username, 0, 0, event.Message()), nil
	case *events.ServiceCatalogUpdated:
		return eventnotifications.ReconciliationPayload{ID: eventID, Type: event.Type().String(), StackName: event.StackName,
			Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.SyncManualStarted:
		return eventnotifications.ReconciliationPayload{
			ID: eventID, Type: event.Type().String(), Username: event.Username,
			Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.WebhookReceived:
		return eventnotifications.ReconciliationPayload{ID: eventID, Type: event.Type().String(), Queued: event.Queued,
			Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.NetworkCreated:
		return eventnotifications.ReconciliationPayload{
			ID: eventID, Type: event.Type().String(), NetworkName: event.NetworkName,
			NetworkID: event.NetworkID, Driver: event.Driver, Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.UserAuthenticated:
		return eventnotifications.SecurityPayload{ID: eventID, Type: event.Type().String(), Username: event.Username,
			Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.AssistantPromptInjectionDetected:
		return eventnotifications.AssistantPayload{ID: eventID, Type: event.Type().String(), Detector: string(event.Detector),
			Username: event.Username, Message: event.Message(), OccurredAt: occurredAt}, nil
	case *events.SendNotificationFailed:
		return eventnotifications.Payload{ID: eventID, Type: event.Type().String(), EventType: event.EventType.String(),
			Destination: event.Destination, Channel: event.Channel, Error: errorString(event.Error),
			Message: event.Message(), OccurredAt: occurredAt}, nil
	default:
		return nil, fmt.Errorf("notification payload is not defined for %T", envelope.Event)
	}
}

func deploymentPayload(
	id string,
	occurredAt time.Time,
	eventType events.TypeName,
	status, deploymentID, attemptID, stackName, commit string,
	services []compose.Service,
) eventnotifications.DeploymentPayload {
	payload := eventnotifications.DeploymentPayload{
		ID: id, DeploymentID: deploymentID, AttemptID: attemptID,
		Type: string(eventType), Status: status, StackName: stackName,
		Commit: commit, OccurredAt: occurredAt,
	}
	if len(services) > 0 {
		payload.Services = make([]eventnotifications.DeploymentService, 0, len(services))
	}
	for _, service := range services {
		payload.Services = append(payload.Services, eventnotifications.DeploymentService{
			Name: service.Name, Image: service.Image,
		})
	}
	return payload
}

func servicePayload(
	id string,
	occurredAt time.Time,
	eventType, stackName, serviceName, commit, username string,
	previous, current uint64,
	message string,
) eventnotifications.ServicePayload {
	return eventnotifications.ServicePayload{ID: id, Type: eventType, StackName: stackName, ServiceName: serviceName,
		Commit: commit, Username: username, PreviousReplicas: previous, CurrentReplicas: current,
		Message: message, OccurredAt: occurredAt}
}

func errorString(err error) string {
	return faults.SafeDiagnostic(err)
}
