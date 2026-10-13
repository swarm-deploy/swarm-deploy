package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// Subscriber translates deployment events into alert lifecycle changes.
type Subscriber struct {
	mu        sync.Mutex
	store     modelstore.Store
	publisher interface {
		Publish(context.Context, events.Event) error
	}
	recipients func(model.AlertKind) []events.AlertRecipient
	now        func() time.Time
	newID      func() string
}

// NewSubscriber creates a deployment alert subscriber.
func NewSubscriber(store modelstore.Store, publisher interface {
	Publish(context.Context, events.Event) error
},
	recipients func(model.AlertKind) []events.AlertRecipient) *Subscriber {
	return &Subscriber{store: store, publisher: publisher, recipients: recipients, now: time.Now, newID: uuid.NewString}
}

// Name returns the subscriber name used in dispatcher logs.
func (s *Subscriber) Name() string { return "alert-management" }

// Handle applies supported deployment events to alert state.
func (s *Subscriber) Handle(ctx context.Context, event events.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch deployment := event.Event.(type) {
	case *events.NodeDisconnected:
		return s.handleNode(ctx, event.ID, deployment.NodeID, true)
	case *events.NodeConnected:
		return s.handleNode(ctx, event.ID, deployment.NodeID, false)
	case *events.DeployFailed:
		return s.handleFailed(ctx, event.ID, deployment.StackName, "Deployment failed", deployFailureDetails(deployment))
	case *events.DeployPreparationFailed:
		return s.handleFailed(ctx, event.ID, deployment.StackName, "Deployment failed", failureDetails{
			message:       safeFailureMessage(deployment.Error, deployment.ErrorCode, deployment.Stage),
			hasDiagnostic: hasUsefulDiagnostic(deployment.Error, deployment.ErrorCode),
			stage:         deployment.Stage, code: deployment.ErrorCode, commit: deployment.Commit,
		})
	case *events.DeployInterrupted:
		return s.handleFailed(ctx, event.ID, deployment.StackName, "Deployment outcome unknown", failureDetails{
			message:       safeFailureMessage(deployment.Error, deployment.Reason, deployment.Stage),
			hasDiagnostic: hasUsefulDiagnostic(deployment.Error, deployment.Reason),
			stage:         deployment.Stage, code: deployment.Reason, deploymentID: deployment.DeploymentID,
			commit: deployment.Commit,
		})
	case *events.DeploySuccess:
		return s.handleSucceeded(ctx, event.ID, deployment)
	default:
		return nil
	}
}

type failureDetails struct {
	message       string
	hasDiagnostic bool
	stage         string
	code          string
	deploymentID  string
	commit        string
}

func (s *Subscriber) handleFailed(
	ctx context.Context, eventID, stackName, title string, details failureDetails,
) error {
	fingerprint := model.DeployFailedFingerprint(stackName)
	now := s.now()
	if eventID == "" {
		eventID = s.newID()
	}
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil && !errors.Is(err, modelstore.ErrAlertNotFound) {
		return fmt.Errorf("find open deployment alert: %w", err)
	}
	if err == nil {
		alert.Occurrences++
		alert.UpdatedAt = now
		alert.LatestEventID = eventID
		if details.hasDiagnostic || alert.Message == "" {
			alert.Message = details.message
		}
		alert.Title = title
		alert.FailureStage, alert.ErrorCode = details.stage, details.code
		alert.DeploymentID, alert.Commit = details.deploymentID, details.commit
		if err = s.store.Update(ctx, alert); err != nil {
			return fmt.Errorf("update deployment alert: %w", err)
		}
		return nil
	}

	alert = model.Alert{
		ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindDeployFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: stackName,
		Status: model.AlertStatusOpen, Title: title, Message: details.message,
		FailureStage: details.stage, ErrorCode: details.code,
		DeploymentID: details.deploymentID, Commit: details.commit,
		Occurrences: 1, OpenedAt: now, UpdatedAt: now, OpenEventID: eventID, LatestEventID: eventID,
	}
	err = s.store.Create(ctx, alert)
	if err == nil {
		return s.publishOpened(ctx, alert)
	}
	if !errors.Is(err, modelstore.ErrOpenAlertExists) {
		return fmt.Errorf("create deployment alert: %w", err)
	}
	return s.updateConcurrentFailure(ctx, fingerprint, eventID, now, alert, details.hasDiagnostic, err)
}

func (s *Subscriber) updateConcurrentFailure(
	ctx context.Context,
	fingerprint string,
	eventID string,
	now time.Time,
	incoming model.Alert,
	hasDiagnostic bool,
	createErr error,
) error {
	existing, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, modelstore.ErrAlertNotFound) {
			return createErr
		}
		return fmt.Errorf("refetch concurrent deployment alert: %w", err)
	}
	existing.Occurrences++
	existing.UpdatedAt = now
	existing.LatestEventID = eventID
	if hasDiagnostic || existing.Message == "" {
		existing.Message = incoming.Message
	}
	existing.Title = incoming.Title
	existing.FailureStage, existing.ErrorCode = incoming.FailureStage, incoming.ErrorCode
	existing.DeploymentID, existing.Commit = incoming.DeploymentID, incoming.Commit
	return s.store.Update(ctx, existing)
}

func (s *Subscriber) handleSucceeded(ctx context.Context, eventID string, event *events.DeploySuccess) error {
	alert, err := s.store.FindOpenByFingerprint(ctx, model.DeployFailedFingerprint(event.StackName))
	if err != nil {
		if errors.Is(err, modelstore.ErrAlertNotFound) {
			return nil
		}
		return fmt.Errorf("find open deployment alert: %w", err)
	}
	now := s.now()
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = now
	alert.ResolvedAt = &now
	alert.Resolution = &model.AlertResolution{
		Reason:  model.ResolutionReasonRecovered,
		Message: "Deployment completed successfully",
		EventID: eventReferenceID(eventID, s.newID),
	}
	if err = s.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("resolve deployment alert: %w", err)
	}
	return s.publishResolved(ctx, alert)
}

func (s *Subscriber) publishOpened(ctx context.Context, alert model.Alert) error {
	return s.publisher.Publish(ctx, &events.AlertOpened{Alert: alertSnapshot(alert), Recipients: s.recipients(alert.Kind)})
}

func (s *Subscriber) publishResolved(ctx context.Context, alert model.Alert) error {
	return s.publisher.Publish(ctx, &events.AlertResolved{
		Alert: alertSnapshot(alert), Recipients: s.recipients(alert.Kind),
	})
}

func alertSnapshot(alert model.Alert) events.AlertSnapshot {
	snapshot := events.AlertSnapshot{ID: alert.ID, Kind: string(alert.Kind), Title: alert.Title,
		Message: alert.Message, ResourceType: string(alert.ResourceType), ResourceID: alert.ResourceID,
		OpenedAt: alert.OpenedAt, ResolvedAt: alert.ResolvedAt}
	if alert.Resolution != nil {
		snapshot.Resolution = alert.Resolution.Message
		snapshot.ResolutionReason = string(alert.Resolution.Reason)
	}
	return snapshot
}

func eventReferenceID(eventID string, fallback func() string) string {
	if eventID != "" {
		return eventID
	}
	return fallback()
}

func deployFailureDetails(event *events.DeployFailed) failureDetails {
	diagnostic := event.Error
	if diagnostic == nil {
		if logs := strings.TrimSpace(strings.Join(event.Logs, "\n")); logs != "" {
			diagnostic = errors.New(logs)
		}
	}
	return failureDetails{
		message:       safeFailureMessage(diagnostic, event.ErrorCode, event.Stage),
		hasDiagnostic: hasUsefulDiagnostic(diagnostic, event.ErrorCode),
		stage:         event.Stage, code: event.ErrorCode, deploymentID: event.DeploymentID, commit: event.Commit,
	}
}

func hasUsefulDiagnostic(err error, code string) bool {
	diagnostic := faults.SafeDiagnostic(err)
	return diagnostic != "" && diagnostic != code
}

func safeFailureMessage(err error, code, stage string) string {
	if diagnostic := faults.SafeDiagnostic(err); diagnostic != "" && diagnostic != code {
		return diagnostic
	}
	if stage == "" {
		stage = "an unknown stage"
	}
	return fmt.Sprintf("Deployment failed during %s. No detailed diagnostic was recorded.", stage)
}
