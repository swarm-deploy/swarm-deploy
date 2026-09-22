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
)

// Subscriber translates deployment events into alert lifecycle changes.
type Subscriber struct {
	mu    sync.Mutex
	store modelstore.Store
	now   func() time.Time
	newID func() string
}

// NewSubscriber creates a deployment alert subscriber.
func NewSubscriber(store modelstore.Store) *Subscriber {
	return &Subscriber{store: store, now: time.Now, newID: uuid.NewString}
}

// Name returns the subscriber name used in dispatcher logs.
func (s *Subscriber) Name() string { return "alert-management" }

// Slow reports that persistence should run on the slow event queue.
func (s *Subscriber) Slow() bool { return true }

// Handle applies supported deployment events to alert state.
func (s *Subscriber) Handle(ctx context.Context, event events.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch deployment := event.Event.(type) {
	case *events.DeployFailed:
		return s.handleFailed(ctx, event.ID, deployment)
	case *events.DeploySuccess:
		return s.handleSucceeded(ctx, event.ID, deployment)
	default:
		return nil
	}
}

func (s *Subscriber) handleFailed(ctx context.Context, eventID string, event *events.DeployFailed) error {
	fingerprint := model.DeployFailedFingerprint(event.StackName)
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
		alert.Message = deploymentFailureMessage(event)
		if err = s.store.Update(ctx, alert); err != nil {
			return fmt.Errorf("update deployment alert: %w", err)
		}
		return nil
	}

	alert = model.Alert{
		ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindDeployFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: event.StackName,
		Status: model.AlertStatusOpen, Title: "Deployment failed", Message: deploymentFailureMessage(event),
		Occurrences: 1, OpenedAt: now, UpdatedAt: now, OpenEventID: eventID, LatestEventID: eventID,
	}
	err = s.store.Create(ctx, alert)
	if err == nil {
		return nil
	}
	if !errors.Is(err, modelstore.ErrOpenAlertExists) {
		return fmt.Errorf("create deployment alert: %w", err)
	}
	return s.updateConcurrentFailure(ctx, fingerprint, eventID, now, alert.Message, err)
}

func (s *Subscriber) updateConcurrentFailure(
	ctx context.Context,
	fingerprint string,
	eventID string,
	now time.Time,
	message string,
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
	existing.Message = message
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
	return nil
}

func eventReferenceID(eventID string, fallback func() string) string {
	if eventID != "" {
		return eventID
	}
	return fallback()
}

func deploymentFailureMessage(event *events.DeployFailed) string {
	if event.Error != nil && strings.TrimSpace(event.Error.Error()) != "" {
		return event.Error.Error()
	}
	if logs := strings.TrimSpace(strings.Join(event.Logs, "\n")); logs != "" {
		return logs
	}
	return event.Message()
}
