// Package codec defines the explicitly allowlisted, versioned durable event format.
package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Version identifies this durable payload schema.
const Version = 1

// Encode copies approved fields only. It never marshals Event, Details, errors,
// logs, prompts, environment or Compose definitions.
func Encode(event events.Event) ([]byte, error) {
	if event == nil || (reflect.ValueOf(event).Kind() == reflect.Pointer && reflect.ValueOf(event).IsNil()) {
		return nil, errors.New("cannot encode a nil event")
	}
	switch e := event.(type) {
	case *events.ServiceCatalogUpdated:
		return json.Marshal(servicePayload{Stack: e.StackName})
	case *events.DeploySuccess:
		return encodeDeployment(e.DeployEvent)
	case *events.DeployFailed:
		return encodeDeployment(e.DeployEvent)
	case *events.DeployPreparationFailed:
		if e.ErrorCode != "preparation_failed" {
			return nil, errors.New("unsupported deployment preparation failure code")
		}
		return encodeDeploymentFailure(e.StackName, e.Commit, "", e.ErrorCode, e.Services)
	case *events.DeployInterrupted:
		if e.Reason != "process_interrupted" {
			return nil, errors.New("unsupported deployment interruption reason")
		}
		return encodeDeploymentFailure(e.StackName, e.Commit, e.DeploymentID, e.Reason, e.Services)
	case *events.NodeJoined:
		return json.Marshal(nodePayload{ID: e.NodeID, Name: e.NodeName, Role: e.Role})
	case *events.NodeConnected:
		return json.Marshal(nodePayload{ID: e.NodeID, Name: e.NodeName, Status: e.Status})
	case *events.NodeDisconnected:
		return json.Marshal(nodePayload{ID: e.NodeID, Name: e.NodeName, Status: e.Status})
	case *events.ServiceMissed:
		return json.Marshal(servicePayload{Stack: e.StackName, Name: e.ServiceName, Commit: e.Commit})
	case *events.ServicePruned:
		return json.Marshal(servicePayload{Stack: e.StackName, Name: e.ServiceName, Commit: e.Commit})
	case *events.ServiceReplicasIncreased:
		return json.Marshal(servicePayload{
			Stack: e.StackName, Name: e.ServiceName, Username: e.Username,
			Previous: e.PreviousReplicas, Current: e.CurrentReplicas,
		})
	case *events.ServiceReplicasDecreased:
		return json.Marshal(servicePayload{
			Stack: e.StackName, Name: e.ServiceName, Username: e.Username,
			Previous: e.PreviousReplicas, Current: e.CurrentReplicas,
		})
	case *events.ServiceRestarted:
		return json.Marshal(servicePayload{Stack: e.StackName, Name: e.ServiceName, Username: e.Username})
	case *events.NetworkCreated:
		return json.Marshal(networkPayload{ID: e.NetworkID, Name: e.NetworkName, Driver: e.Driver})
	case *events.SyncManualStarted:
		return json.Marshal(userPayload{Username: e.TriggeredBy})
	case *events.UserAuthenticated:
		return json.Marshal(userPayload{Username: e.Username})
	case *events.WebhookReceived:
		return json.Marshal(webhookPayload{Queued: e.Queued})
	case *events.AssistantPromptInjectionDetected:
		return json.Marshal(userPayload{Username: e.Username, Detector: e.Detector})
	case *events.SendNotificationFailed:
		return json.Marshal(notificationPayload{EventType: e.EventType, Destination: e.Destination, Channel: e.Channel})
	default:
		return nil, fmt.Errorf("unsupported persistent event %T", event)
	}
}

// Decode rejects unsupported schema versions and types without losing the delivery.
func Decode(typ events.TypeName, version int, payload []byte) (events.Event, error) {
	if version != Version {
		return nil, fmt.Errorf("unsupported event schema version %d", version)
	}
	switch typ {
	case events.TypeNameServiceCatalogUpdated:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServiceCatalogUpdated{StackName: p.Stack}
		})
	case events.TypeNameDeploySuccess:
		return decodeAs(payload, func(p deployPayload) events.Event {
			return &events.DeploySuccess{DeployEvent: p.deployment()}
		})
	case events.TypeNameDeployFailed:
		return decodeAs(payload, func(p deployPayload) events.Event {
			return &events.DeployFailed{
				DeployEvent: p.deployment(),
				Error:       errors.New("deployment failed; sensitive diagnostic output omitted"),
			}
		})
	case events.TypeNameDeployPreparationFailed:
		return decodeAs(payload, func(p deploymentFailurePayload) events.Event {
			return &events.DeployPreparationFailed{
				StackName: p.Stack, Commit: p.Commit, Services: p.services(), ErrorCode: p.Code,
				Error: errors.New("deployment preparation failed; sensitive diagnostic output omitted"),
			}
		})
	case events.TypeNameDeployInterrupted:
		return decodeAs(payload, func(p deploymentFailurePayload) events.Event {
			return &events.DeployInterrupted{
				DeploymentID: p.DeploymentID, StackName: p.Stack, Commit: p.Commit,
				Services: p.services(), Reason: p.Code,
				Error: errors.New("deployment interrupted; external apply outcome unknown"),
			}
		})
	case events.TypeNameNodeJoined:
		return decodeAs(payload, func(p nodePayload) events.Event {
			return &events.NodeJoined{NodeID: p.ID, NodeName: p.Name, Role: p.Role}
		})
	case events.TypeNameNodeConnected:
		return decodeAs(payload, func(p nodePayload) events.Event {
			return &events.NodeConnected{NodeID: p.ID, NodeName: p.Name, Status: p.Status}
		})
	case events.TypeNameNodeDisconnected:
		return decodeAs(payload, func(p nodePayload) events.Event {
			return &events.NodeDisconnected{NodeID: p.ID, NodeName: p.Name, Status: p.Status}
		})
	case events.TypeNameServiceMissed:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServiceMissed{StackName: p.Stack, ServiceName: p.Name, Commit: p.Commit}
		})
	case events.TypeNameServicePruned:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServicePruned{StackName: p.Stack, ServiceName: p.Name, Commit: p.Commit}
		})
	case events.TypeNameServiceReplicasIncreased:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServiceReplicasIncreased{
				StackName: p.Stack, ServiceName: p.Name, Username: p.Username,
				PreviousReplicas: p.Previous, CurrentReplicas: p.Current,
			}
		})
	case events.TypeNameServiceReplicasDecreased:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServiceReplicasDecreased{
				StackName: p.Stack, ServiceName: p.Name, Username: p.Username,
				PreviousReplicas: p.Previous, CurrentReplicas: p.Current,
			}
		})
	case events.TypeNameServiceRestarted:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServiceRestarted{StackName: p.Stack, ServiceName: p.Name, Username: p.Username}
		})
	case events.TypeNameNetworkCreated:
		return decodeAs(payload, func(p networkPayload) events.Event {
			return &events.NetworkCreated{NetworkID: p.ID, NetworkName: p.Name, Driver: p.Driver}
		})
	case events.TypeNameSyncManualStarted:
		return decodeAs(payload, func(p userPayload) events.Event {
			return &events.SyncManualStarted{TriggeredBy: p.Username}
		})
	case events.TypeNameUserAuthenticated:
		return decodeAs(payload, func(p userPayload) events.Event { return &events.UserAuthenticated{Username: p.Username} })
	case events.TypeNameWebhookReceived:
		return decodeAs(payload, func(p webhookPayload) events.Event { return &events.WebhookReceived{Queued: p.Queued} })
	case events.TypeNameAssistantPromptInjectionDetected:
		return decodeAs(payload, func(p userPayload) events.Event {
			return &events.AssistantPromptInjectionDetected{Username: p.Username, Detector: p.Detector}
		})
	case events.TypeNameSendNotificationFailed:
		return decodeAs(payload, func(p notificationPayload) events.Event {
			return &events.SendNotificationFailed{EventType: p.EventType, Destination: p.Destination, Channel: p.Channel}
		})
	default:
		return nil, fmt.Errorf("unsupported persistent event type %q", typ)
	}
}

func decodeAs[T any](payload []byte, event func(T) events.Event) (events.Event, error) {
	var value T
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	return event(value), nil
}

func decode(payload []byte, target any) error {
	if trimmed := bytes.TrimSpace(payload); len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("event payload must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing event payload data")
	}
	return nil
}

type deployPayload struct {
	// DeploymentID references safe input in the deployment repository.
	DeploymentID string `json:"deployment_id,omitempty"`
	// Stack identifies the stack.
	Stack string `json:"stack"`
	// Commit identifies the source revision.
	Commit string `json:"commit"`
	// Services contains only service names and images.
	Services []servicePayload `json:"services"`
}

type deploymentFailurePayload struct {
	// DeploymentID is absent for preparation failures before an attempt exists.
	DeploymentID string `json:"deployment_id,omitempty"`
	// Stack identifies the affected stack.
	Stack string `json:"stack"`
	// Commit identifies the source revision.
	Commit string `json:"commit"`
	// Code is an allowlisted safe failure category.
	Code string `json:"code"`
	// Services contains only service names and images.
	Services []servicePayload `json:"services"`
}

func (p deploymentFailurePayload) services() []compose.Service {
	result := make([]compose.Service, 0, len(p.Services))
	for _, service := range p.Services {
		result = append(result, compose.Service{Name: service.Name, Image: service.Image})
	}
	return result
}

func encodeDeploymentFailure(
	stack, commit, deploymentID, code string,
	services []compose.Service,
) ([]byte, error) {
	payload := deploymentFailurePayload{
		DeploymentID: deploymentID, Stack: stack, Commit: commit, Code: code,
		Services: make([]servicePayload, 0, len(services)),
	}
	for _, service := range services {
		payload.Services = append(payload.Services, servicePayload{Name: service.Name, Image: service.Image})
	}
	return json.Marshal(payload)
}

func (p deployPayload) deployment() events.DeployEvent {
	event := events.DeployEvent{DeploymentID: p.DeploymentID, StackName: p.Stack, Commit: p.Commit}
	for _, service := range p.Services {
		event.Services = append(event.Services, compose.Service{Name: service.Name, Image: service.Image})
	}
	return event
}

func encodeDeployment(event events.DeployEvent) ([]byte, error) {
	p := deployPayload{DeploymentID: event.DeploymentID, Stack: event.StackName, Commit: event.Commit}
	for _, service := range event.Services {
		p.Services = append(p.Services, servicePayload{Name: service.Name, Image: service.Image})
	}
	return json.Marshal(p)
}

type servicePayload struct {
	// Stack identifies the stack.
	Stack string `json:"stack"`
	// Name identifies the service.
	Name string `json:"name"`
	// Image is the image reference, without environment or runtime configuration.
	Image string `json:"image"`
	// Commit identifies the revision.
	Commit string `json:"commit"`
	// Username identifies the initiating user.
	Username string `json:"username"`
	// Previous is the former replica count.
	Previous uint64 `json:"previous"`
	// Current is the requested replica count.
	Current uint64 `json:"current"`
}

type nodePayload struct {
	// ID identifies the node.
	ID string `json:"id"`
	// Name is the hostname.
	Name string `json:"name"`
	// Role is the Swarm role.
	Role string `json:"role"`
	// Status is the observed state.
	Status string `json:"status"`
}

type networkPayload struct {
	// ID identifies the network.
	ID string `json:"id"`
	// Name identifies the network.
	Name string `json:"name"`
	// Driver is the network driver.
	Driver string `json:"driver"`
}

type userPayload struct {
	// Username identifies the initiating user.
	Username string `json:"username"`
	// Detector identifies the detector without persisting its input.
	Detector events.AssistantPromptInjectionDetector `json:"detector"`
}

type webhookPayload struct {
	// Queued records whether reconciliation was scheduled.
	Queued bool `json:"queued"`
}

type notificationPayload struct {
	// EventType identifies the source event.
	EventType events.Type `json:"event_type"`
	// Destination is the notifier kind.
	Destination string `json:"destination"`
	// Channel is the configured channel name.
	Channel string `json:"channel"`
}
