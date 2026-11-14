package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Lifecycle applies alert changes within the caller's transaction.
type Lifecycle interface {
	// Apply applies actions in order and stops on the first error.
	Apply(ctx context.Context, actions []model.AlertAction) error
}

type alertLifecycle struct {
	store      modelstore.Store
	publisher  Publisher
	recipients func(model.AlertKind) []events.AlertRecipient
	now        func() time.Time
	newID      func() string
}

func newLifecycle(
	store modelstore.Store, publisher Publisher, recipients func(model.AlertKind) []events.AlertRecipient,
) *alertLifecycle {
	return &alertLifecycle{
		store: store, publisher: publisher, recipients: recipients, now: time.Now, newID: uuid.NewString,
	}
}

func (l *alertLifecycle) Apply(ctx context.Context, actions []model.AlertAction) error {
	now := l.now()
	for _, action := range actions {
		if err := l.apply(ctx, action, now); err != nil {
			return fmt.Errorf("apply alert action %s: %w", action.Kind, err)
		}
	}
	return nil
}

func (l *alertLifecycle) apply(ctx context.Context, action model.AlertAction, now time.Time) error {
	switch action.Kind {
	case model.RecordFailure:
		return l.recordFailure(ctx, action, now)
	case model.Resolve:
		return l.resolve(ctx, action.Alert, now)
	case model.ResetFailureStreak:
		return l.store.ResetFailureStreak(ctx, action.Alert.Fingerprint)
	case model.ResetFailureStreaksExcept:
		return l.store.ResetFailureStreaksExcept(ctx, action.KeepFailureStreaks)
	default:
		return fmt.Errorf("unknown alert action %q", action.Kind)
	}
}

func (l *alertLifecycle) recordFailure(ctx context.Context, action model.AlertAction, now time.Time) error {
	incoming := action.Alert
	failures := uint64(1)
	if action.TrackFailureStreak {
		var err error
		failures, err = l.store.IncrementFailureStreak(ctx, incoming.Fingerprint)
		if err != nil {
			return fmt.Errorf("increment failure streak for %q: %w", incoming.Fingerprint, err)
		}
	}

	existing, err := l.store.FindOpenByFingerprint(ctx, incoming.Fingerprint)
	if err == nil {
		// Repeat failures refresh only the diagnostics this alert kind reports.
		switch incoming.Kind {
		case model.AlertKindSyncFailed:
		case model.AlertKindGitRepositoryUnavailable:
			incoming.Title, incoming.FailureStage = existing.Title, existing.FailureStage
			incoming.DeploymentID, incoming.Commit = existing.DeploymentID, existing.Commit
		case model.AlertKindServiceDrift:
			incoming.Title, incoming.FailureStage = existing.Title, existing.FailureStage
			incoming.ErrorCode, incoming.DeploymentID = existing.ErrorCode, existing.DeploymentID
		case model.AlertKindNodeDisconnected:
			incoming.Title, incoming.Message = existing.Title, existing.Message
			incoming.FailureStage, incoming.ErrorCode = existing.FailureStage, existing.ErrorCode
			incoming.DeploymentID, incoming.Commit = existing.DeploymentID, existing.Commit
		}
		incoming.LatestEventID = l.eventReferenceID(incoming.Kind, incoming.LatestEventID)
		return l.updateFailure(ctx, existing, incoming, action.PreserveExistingMessage, now)
	}
	if !errors.Is(err, modelstore.ErrAlertNotFound) {
		return fmt.Errorf("find open alert %q: %w", incoming.Fingerprint, err)
	}
	if failures < action.OpenAfter {
		return nil
	}

	incoming.ID = l.newID()
	incoming.Status = model.AlertStatusOpen
	incoming.ResolvedAt, incoming.Resolution = nil, nil
	incoming.Occurrences = failures
	incoming.OpenedAt, incoming.UpdatedAt = now, now
	incoming.LatestEventID = l.eventReferenceID(incoming.Kind, incoming.LatestEventID)
	incoming.OpenEventID = incoming.LatestEventID
	incoming.Findings = nil
	appendFindings(&incoming, action.Alert.Findings, now)
	createErr := l.store.Create(ctx, incoming)
	if createErr == nil {
		return l.publisher.Publish(ctx, &events.AlertOpened{
			Alert: alertSnapshot(incoming), Recipients: l.recipients(incoming.Kind),
		})
	}
	if incoming.Kind != model.AlertKindNodeDisconnected && errors.Is(createErr, modelstore.ErrOpenAlertExists) {
		existing, err = l.store.FindOpenByFingerprint(ctx, incoming.Fingerprint)
		if err == nil {
			return l.updateFailure(ctx, existing, incoming, action.PreserveExistingMessage, now)
		}
		if !errors.Is(err, modelstore.ErrAlertNotFound) {
			return fmt.Errorf("refetch concurrent alert %q: %w", incoming.Fingerprint, err)
		}
	}
	return fmt.Errorf("create alert %q: %w", incoming.Fingerprint, createErr)
}

func (l *alertLifecycle) updateFailure(
	ctx context.Context, alert, incoming model.Alert, preserveMessage bool, now time.Time,
) error {
	alert.Occurrences++
	alert.UpdatedAt = now
	alert.LatestEventID = incoming.LatestEventID
	if !preserveMessage || alert.Message == "" {
		alert.Message = incoming.Message
	}
	alert.Title = incoming.Title
	alert.FailureStage, alert.ErrorCode = incoming.FailureStage, incoming.ErrorCode
	alert.DeploymentID, alert.Commit = incoming.DeploymentID, incoming.Commit
	appendFindings(&alert, incoming.Findings, now)
	if err := l.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("update alert %q: %w", alert.Fingerprint, err)
	}
	return nil
}

func appendFindings(alert *model.Alert, findings []model.Finding, now time.Time) {
	for _, finding := range findings {
		if finding.Code == "" || slices.ContainsFunc(alert.Findings, func(existing model.Finding) bool {
			return existing.Code == finding.Code && existing.Summary == finding.Summary
		}) {
			continue
		}
		finding.DetectedAt = now.UTC()
		alert.Findings = append(alert.Findings, finding)
	}
}

func (l *alertLifecycle) resolve(ctx context.Context, incoming model.Alert, now time.Time) error {
	alert, err := l.store.FindOpenByFingerprint(ctx, incoming.Fingerprint)
	if errors.Is(err, modelstore.ErrAlertNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find open alert %q: %w", incoming.Fingerprint, err)
	}
	if incoming.Resolution == nil {
		return fmt.Errorf("missing resolution for alert %q", incoming.Fingerprint)
	}
	resolution := *incoming.Resolution
	resolution.EventID = l.eventReferenceID(alert.Kind, resolution.EventID)
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt, alert.ResolvedAt = now, &now
	alert.Resolution = &resolution
	if err = l.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("resolve alert %q: %w", alert.Fingerprint, err)
	}
	return l.publisher.Publish(ctx, &events.AlertResolved{
		Alert: alertSnapshot(alert), Recipients: l.recipients(alert.Kind),
	})
}

func (l *alertLifecycle) eventReferenceID(kind model.AlertKind, eventID string) string {
	if eventID == "" && kind != model.AlertKindNodeDisconnected {
		return l.newID()
	}
	return eventID
}
