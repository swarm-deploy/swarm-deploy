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
	alert := action.Alert
	failures := uint64(1)
	if action.TrackFailureStreak {
		var err error
		failures, err = l.store.IncrementFailureStreak(ctx, alert.Fingerprint)
		if err != nil {
			return fmt.Errorf("increment failure streak for %q: %w", alert.Fingerprint, err)
		}
	}
	if alert.ID == "" && failures < action.OpenAfter {
		return nil
	}

	alert.UpdatedAt = now
	alert.LatestEventID = l.eventReferenceID(alert.Kind, alert.LatestEventID)
	appendFindings(&alert, action.Findings, now)
	if alert.ID != "" {
		alert.Occurrences++
		if err := l.store.Update(ctx, alert); err != nil {
			return fmt.Errorf("update alert %q: %w", alert.Fingerprint, err)
		}
		return nil
	}

	alert.ID = l.newID()
	alert.Status = model.AlertStatusOpen
	alert.Occurrences = failures
	alert.OpenedAt = now
	alert.OpenEventID = alert.LatestEventID
	if err := l.store.Create(ctx, alert); err != nil {
		return fmt.Errorf("create alert %q: %w", alert.Fingerprint, err)
	}
	return l.publisher.Publish(ctx, &events.AlertOpened{
		Alert: alertSnapshot(alert), Recipients: l.recipients(alert.Kind),
	})
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
