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
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// Version identifies this durable payload schema.
const Version = 1

// Encode copies approved fields only. It never marshals Event, Details, errors,
// logs, prompts, environment or Compose definitions.
//
//nolint:funlen // Keep the durable event allowlist together so its accepted fields are auditable.
func Encode(event events.Event) ([]byte, error) {
	if event == nil || (reflect.ValueOf(event).Kind() == reflect.Pointer && reflect.ValueOf(event).IsNil()) {
		return nil, errors.New("cannot encode a nil event")
	}
	if payload, internal, err := encodeInternal(event); internal {
		return payload, err
	}
	switch e := event.(type) {
	case *events.ConfigurationChanged:
		return json.Marshal(struct{}{})
	case *events.ServiceCatalogUpdated:
		return json.Marshal(servicePayload{Stack: e.StackName})
	case *events.DeploySuccess:
		return encodeDeployment(e.DeployEvent)
	case *events.DeployFailed:
		code := e.ErrorCode
		if code == "" {
			code = safeFailureCode(e.Error)
		}
		return encodeDeploymentFailure(e.StackName, e.Commit, e.DeploymentID, code, e.Stage,
			faults.SafeDiagnostic(e.Error), temporaryClassification(e.Error), e.Services)
	case *events.DeployPreparationFailed:
		if e.ErrorCode != "preparation_failed" {
			return nil, errors.New("unsupported deployment preparation failure code")
		}
		return encodeDeploymentFailure(e.StackName, e.Commit, "", e.ErrorCode, e.Stage,
			faults.SafeDiagnostic(e.Error), temporaryClassification(e.Error), e.Services)
	case *events.DeployInterrupted:
		if e.Reason != "process_interrupted" && e.Reason != "verification_unknown" {
			return nil, errors.New("unsupported deployment interruption reason")
		}
		return encodeDeploymentFailure(e.StackName, e.Commit, e.DeploymentID, e.Reason, e.Stage,
			faults.SafeDiagnostic(e.Error), temporaryClassification(e.Error), e.Services)
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
	if isDeploymentFailureType(typ) {
		return decodeDeploymentFailure(typ, payload)
	}
	switch typ { //nolint:exhaustive // Failure events are decoded by decodeDeploymentFailure above.
	case events.TypeNameConfigurationChanged:
		return decodeAs(payload, func(struct{}) events.Event { return &events.ConfigurationChanged{} })
	case events.TypeNameAlertOpened:
		return decodeAs(payload, func(p alertOpenedPayload) events.Event {
			return &events.AlertOpened{Alert: p.Alert.snapshot(), Recipients: p.Recipients}
		})
	case events.TypeNameAlertResolved:
		return decodeAs(payload, func(p alertResolvedPayload) events.Event {
			return &events.AlertResolved{Alert: p.Alert.snapshot(), Recipients: p.Recipients}
		})
	case events.TypeNameServiceCatalogUpdated:
		return decodeAs(payload, func(p servicePayload) events.Event {
			return &events.ServiceCatalogUpdated{StackName: p.Stack}
		})
	case events.TypeNameDeploySuccess:
		return decodeAs(payload, func(p deployPayload) events.Event {
			return &events.DeploySuccess{DeployEvent: p.deployment()}
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

func isDeploymentFailureType(typ events.TypeName) bool {
	return typ == events.TypeNameDeployFailed ||
		typ == events.TypeNameDeployPreparationFailed ||
		typ == events.TypeNameDeployInterrupted
}

func decodeDeploymentFailure(typ events.TypeName, payload []byte) (events.Event, error) {
	return decodeAs(payload, func(p deploymentFailurePayload) events.Event {
		diagnostic := diagnosticError(p.Diagnostic, p.Temporary)
		switch typ { //nolint:exhaustive // isDeploymentFailureType restricts this helper to these event types.
		case events.TypeNameDeployFailed:
			return &events.DeployFailed{
				DeployEvent: events.DeployEvent{
					DeploymentID: p.DeploymentID, StackName: p.Stack, Commit: p.Commit, Services: p.services(),
				},
				Error: diagnostic, ErrorCode: safeFailureCode(errors.New(p.Code)), Stage: p.Stage,
			}
		case events.TypeNameDeployPreparationFailed:
			return &events.DeployPreparationFailed{
				StackName: p.Stack, Commit: p.Commit, Services: p.services(), ErrorCode: p.Code,
				Error: diagnostic, Stage: p.Stage,
			}
		default:
			return &events.DeployInterrupted{
				DeploymentID: p.DeploymentID, StackName: p.Stack, Commit: p.Commit,
				Services: p.services(), Reason: p.Code, Error: diagnostic, Stage: p.Stage,
			}
		}
	})
}

type classifiedDiagnosticError struct {
	diagnostic string
	temporary  bool
}

func (e classifiedDiagnosticError) Error() string { return e.diagnostic }

func (e classifiedDiagnosticError) Temporary() bool { return e.temporary }

func diagnosticError(diagnostic string, temporary bool) error {
	return classifiedDiagnosticError{diagnostic: diagnostic, temporary: temporary}
}

func temporaryClassification(err error) bool {
	var classified faults.Error
	if !errors.As(err, &classified) {
		return false
	}
	return classified.Temporary()
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
	// Stage identifies the failing pipeline stage.
	Stage string `json:"stage,omitempty"`
	// Diagnostic contains bounded secret-masked technical context.
	Diagnostic string `json:"diagnostic,omitempty"`
	// Temporary preserves an explicit technical classification without persisting the error structure.
	Temporary bool `json:"temporary"`
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

func safeFailureCode(err error) string {
	if err != nil {
		switch err.Error() {
		case "policy_rejected", "init_failed", "apply_failed", "prune_failed":
			return err.Error()
		}
	}
	return "apply_failed"
}

func encodeDeploymentFailure(
	stack, commit, deploymentID, code, stage, diagnostic string, temporary bool,
	services []compose.Service,
) ([]byte, error) {
	payload := deploymentFailurePayload{
		DeploymentID: deploymentID, Stack: stack, Commit: commit, Code: code, Stage: stage,
		Diagnostic: diagnostic, Temporary: temporary,
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
