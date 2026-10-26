package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// Subscriber translates deployment events into alert lifecycle changes.
type Subscriber struct {
	mu        sync.Mutex
	store     modelstore.Store
	stacks    config.StackProvider
	publisher interface {
		Publish(context.Context, events.Event) error
	}
	recipients func(model.AlertKind) []events.AlertRecipient
	now        func() time.Time
	newID      func() string
}

const temporaryFailureThreshold = 3

// NewSubscriber creates a deployment alert subscriber.
func NewSubscriber(store modelstore.Store, publisher interface {
	Publish(context.Context, events.Event) error
},
	recipients func(model.AlertKind) []events.AlertRecipient,
	stacks config.StackProvider,
) *Subscriber {
	return &Subscriber{
		store: store, stacks: stacks, publisher: publisher, recipients: recipients,
		now: time.Now, newID: uuid.NewString,
	}
}

// Name returns the subscriber name used in dispatcher logs.
func (s *Subscriber) Name() string { return "alert-management" }

// Handle applies supported deployment events to alert state.
func (s *Subscriber) Handle(ctx context.Context, event events.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch deployment := event.Event.(type) {
	case *events.ConfigurationChanged:
		if deployment.PreviousRepository != "" {
			return s.handleGitRepositoryChanged(ctx, event.ID, deployment)
		}
		return s.handleConfigurationChanged(ctx, event.ID)
	case *events.NodeDisconnected:
		return s.handleNode(ctx, event.ID, deployment.NodeID, true)
	case *events.NodeConnected:
		return s.handleNode(ctx, event.ID, deployment.NodeID, false)
	case *events.DeployFailed:
		return s.handleFailed(ctx, event.ID, deployment.StackName, "Synchronization failed",
			deployFailureDetails(deployment), isTemporary(deployment.Error))
	case *events.DeployPreparationFailed:
		return s.handleFailed(ctx, event.ID, deployment.StackName, "Synchronization failed", failureDetails{
			message:       safeFailureMessage(deployment.Error, deployment.ErrorCode, deployment.Stage),
			hasDiagnostic: hasUsefulDiagnostic(deployment.Error, deployment.ErrorCode),
			stage:         deployment.Stage, code: deployment.ErrorCode, commit: deployment.Commit,
		}, isTemporary(deployment.Error))
	case *events.DeployInterrupted:
		return s.handleFailed(ctx, event.ID, deployment.StackName, "Synchronization outcome unknown", failureDetails{
			message:       safeUnknownOutcomeMessage(deployment.Error, deployment.Reason, deployment.Stage),
			hasDiagnostic: hasUsefulDiagnostic(deployment.Error, deployment.Reason),
			stage:         deployment.Stage, code: deployment.Reason, deploymentID: deployment.DeploymentID,
			commit: deployment.Commit,
		}, isTemporary(deployment.Error))
	case *events.DeploySuccess:
		return s.handleSucceeded(ctx, event.ID, deployment)
	case *events.GitPullFailed:
		return s.handleGitPullFailed(ctx, event.ID, deployment)
	case *events.GitPullSucceeded:
		return s.handleGitPullSucceeded(ctx, event.ID, deployment)
	case *events.ServiceMissed:
		return s.handleServiceDrift(ctx, event.ID, deployment.StackName, deployment.ServiceName,
			deployment.Commit, "service is missing", deployment.Resolved)
	case *events.ServiceDrifted:
		return s.handleServiceDrift(ctx, event.ID, deployment.StackName, deployment.ServiceName,
			deployment.Commit, deployment.Reason, deployment.Resolved)
	case *events.ServiceDriftFixed:
		return s.handleServiceDriftFixed(ctx, event.ID, deployment.StackName, deployment.ServiceName)
	case *events.ServiceRemovedFromDesired:
		return s.handleServiceRemovedFromDesired(ctx, event.ID, deployment.StackName, deployment.ServiceName)
	default:
		return nil
	}
}

func (s *Subscriber) handleServiceDrift(
	ctx context.Context, eventID, stackName, serviceName, commit, reason string, resolved bool,
) error {
	if resolved {
		return nil
	}
	resourceID := stackName + "/" + serviceName
	fingerprint := model.ServiceDriftFingerprint(stackName, serviceName)
	now := s.now()
	eventID = eventReferenceID(eventID, s.newID)
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil && !errors.Is(err, modelstore.ErrAlertNotFound) {
		return fmt.Errorf("find open service drift alert: %w", err)
	}
	if err == nil {
		alert.Occurrences++
		alert.UpdatedAt = now
		alert.LatestEventID = eventID
		alert.Message = reason
		alert.Commit = commit
		if err = s.store.Update(ctx, alert); err != nil {
			return fmt.Errorf("update service drift alert: %w", err)
		}
		return nil
	}

	alert = model.Alert{
		ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindServiceDrift,
		ResourceType: model.ResourceTypeService, ResourceID: resourceID,
		Status: model.AlertStatusOpen, Title: "Service drift detected", Message: reason,
		Commit: commit, Occurrences: 1, OpenedAt: now, UpdatedAt: now,
		OpenEventID: eventID, LatestEventID: eventID,
	}
	if err = s.store.Create(ctx, alert); err == nil {
		return s.publishOpened(ctx, alert)
	}
	if !errors.Is(err, modelstore.ErrOpenAlertExists) {
		return fmt.Errorf("create service drift alert: %w", err)
	}
	return s.updateConcurrentFailure(ctx, fingerprint, eventID, now, alert, true, err)
}

func (s *Subscriber) handleServiceDriftFixed(
	ctx context.Context, eventID, stackName, serviceName string,
) error {
	fingerprint := model.ServiceDriftFingerprint(stackName, serviceName)
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, modelstore.ErrAlertNotFound) {
			return nil
		}
		return fmt.Errorf("find open service drift alert: %w", err)
	}
	now := s.now()
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = now
	alert.ResolvedAt = &now
	alert.Resolution = &model.AlertResolution{
		Reason: model.ResolutionReasonRecovered, Message: "Service drift resolved",
		EventID: eventReferenceID(eventID, s.newID),
	}
	if err = s.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("resolve service drift alert: %w", err)
	}
	return s.publishResolved(ctx, alert)
}

func (s *Subscriber) handleServiceRemovedFromDesired(
	ctx context.Context, eventID, stackName, serviceName string,
) error {
	fingerprint := model.ServiceDriftFingerprint(stackName, serviceName)
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, modelstore.ErrAlertNotFound) {
			return nil
		}
		return fmt.Errorf("find removed desired service alert: %w", err)
	}
	return s.resolveRemovedAlert(ctx, alert, eventID, s.now(), "Service removed from desired configuration")
}

func (s *Subscriber) handleGitPullFailed(ctx context.Context, eventID string, event *events.GitPullFailed) error {
	fingerprint := model.GitRepositoryUnavailableFingerprint(event.Repository)
	failures, err := s.store.IncrementFailureStreak(ctx, fingerprint)
	if err != nil {
		return fmt.Errorf("increment Git repository failure streak: %w", err)
	}
	now := s.now()
	if eventID == "" {
		eventID = s.newID()
	}
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil && !errors.Is(err, modelstore.ErrAlertNotFound) {
		return fmt.Errorf("find open Git repository alert: %w", err)
	}
	if err == nil {
		alert.Occurrences++
		alert.UpdatedAt = now
		alert.LatestEventID = eventID
		alert.Message = event.Description
		alert.ErrorCode = string(event.Category)
		if err = s.store.Update(ctx, alert); err != nil {
			return fmt.Errorf("update Git repository alert: %w", err)
		}
		return nil
	}
	if event.Category == events.GitFailureNetwork && failures < temporaryFailureThreshold {
		return nil
	}
	alert = model.Alert{
		ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindGitRepositoryUnavailable,
		ResourceType: model.ResourceTypeRepository, ResourceID: event.Repository,
		Status: model.AlertStatusOpen, Title: "Git repository unavailable", Message: event.Description,
		FailureStage: "git_pull", ErrorCode: string(event.Category), Occurrences: failures,
		OpenedAt: now, UpdatedAt: now, OpenEventID: eventID, LatestEventID: eventID,
	}
	if err = s.store.Create(ctx, alert); err == nil {
		return s.publishOpened(ctx, alert)
	}
	if !errors.Is(err, modelstore.ErrOpenAlertExists) {
		return fmt.Errorf("create Git repository alert: %w", err)
	}
	return s.updateConcurrentFailure(ctx, fingerprint, eventID, now, alert, true, err)
}

func (s *Subscriber) handleGitPullSucceeded(ctx context.Context, eventID string, event *events.GitPullSucceeded) error {
	fingerprint := model.GitRepositoryUnavailableFingerprint(event.Repository)
	if err := s.store.ResetFailureStreak(ctx, fingerprint); err != nil {
		return fmt.Errorf("reset Git repository failure streak: %w", err)
	}
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, modelstore.ErrAlertNotFound) {
			return nil
		}
		return fmt.Errorf("find open Git repository alert: %w", err)
	}
	now := s.now()
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = now
	alert.ResolvedAt = &now
	alert.Resolution = &model.AlertResolution{
		Reason: model.ResolutionReasonRecovered, Message: "Git repository access recovered",
		EventID: eventReferenceID(eventID, s.newID),
	}
	if err = s.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("resolve Git repository alert: %w", err)
	}
	return s.publishResolved(ctx, alert)
}

func (s *Subscriber) handleGitRepositoryChanged(
	ctx context.Context, eventID string, event *events.ConfigurationChanged,
) error {
	fingerprint := model.GitRepositoryUnavailableFingerprint(event.PreviousRepository)
	if err := s.store.ResetFailureStreak(ctx, fingerprint); err != nil {
		return fmt.Errorf("reset removed Git repository failure streak: %w", err)
	}
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil {
		if errors.Is(err, modelstore.ErrAlertNotFound) {
			return nil
		}
		return fmt.Errorf("find removed Git repository alert: %w", err)
	}
	now := s.now()
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = now
	alert.ResolvedAt = &now
	alert.Resolution = &model.AlertResolution{
		Reason: model.ResolutionReasonResourceRemoved, Message: "Git repository removed from configuration",
		EventID: eventReferenceID(eventID, s.newID),
	}
	if err = s.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("resolve removed Git repository alert: %w", err)
	}
	return s.publishResolved(ctx, alert)
}

func (s *Subscriber) handleConfigurationChanged(ctx context.Context, eventID string) error {
	// A retained Outbox event may be delivered before Git is checked out on restart.
	// Returning an error leaves the delivery retryable instead of resolving all
	// stack alerts against the temporarily empty startup configuration.
	if readiness, ok := s.stacks.(interface{ Ready() bool }); ok && !readiness.Ready() {
		return errors.New("desired configuration has not completed its initial reload")
	}

	desired := make(map[string]struct{})
	desiredFingerprints := make([]string, 0)
	for _, stack := range s.stacks.Stacks() {
		desired[stack.Name] = struct{}{}
		desiredFingerprints = append(desiredFingerprints, model.SyncFailedFingerprint(stack.Name))
	}
	if err := s.store.ResetFailureStreaksExcept(ctx, desiredFingerprints); err != nil {
		return fmt.Errorf("reset deployment failure streaks for removed stacks: %w", err)
	}
	alerts, err := s.store.List(ctx, modelstore.ListFilter{
		Status: model.AlertStatusOpen,
	})
	if err != nil {
		return fmt.Errorf("list open alerts for configuration reconciliation: %w", err)
	}
	now := s.now()
	for _, alert := range alerts {
		stackName, relevant := alertStackName(alert)
		if !relevant {
			continue
		}
		if _, exists := desired[stackName]; exists {
			continue
		}
		message := "Stack removed from desired configuration"
		if alert.Kind == model.AlertKindServiceDrift {
			message = "Service removed with stack from desired configuration"
		}
		if err = s.resolveRemovedAlert(ctx, alert, eventID, now, message); err != nil {
			return err
		}
	}
	return nil
}

func alertStackName(alert model.Alert) (string, bool) {
	switch {
	case alert.ResourceType == model.ResourceTypeStack:
		return alert.ResourceID, true
	case alert.Kind == model.AlertKindServiceDrift && alert.ResourceType == model.ResourceTypeService:
		stackName, _, ok := strings.Cut(alert.ResourceID, "/")
		return stackName, ok
	default:
		return "", false
	}
}

func (s *Subscriber) resolveRemovedAlert(
	ctx context.Context, alert model.Alert, eventID string, now time.Time, message string,
) error {
	alert.Status = model.AlertStatusResolved
	alert.UpdatedAt = now
	alert.ResolvedAt = &now
	alert.Resolution = &model.AlertResolution{
		Reason: model.ResolutionReasonResourceRemoved, Message: message,
		EventID: eventReferenceID(eventID, s.newID),
	}
	if err := s.store.Update(ctx, alert); err != nil {
		return fmt.Errorf("resolve alert for removed resource %q: %w", alert.ResourceID, err)
	}
	if err := s.publishResolved(ctx, alert); err != nil {
		return fmt.Errorf("publish resolved alert for removed resource %q: %w", alert.ResourceID, err)
	}
	return nil
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
	ctx context.Context, eventID, stackName, title string, details failureDetails, temporary bool,
) error {
	fingerprint := model.SyncFailedFingerprint(stackName)
	if s.stackRemoved(stackName) {
		if err := s.store.ResetFailureStreak(ctx, fingerprint); err != nil {
			return fmt.Errorf("reset deployment failure streak for removed stack: %w", err)
		}
		return nil
	}
	failures, err := s.store.IncrementFailureStreak(ctx, fingerprint)
	if err != nil {
		return fmt.Errorf("increment deployment failure streak: %w", err)
	}
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
	if temporary && failures < temporaryFailureThreshold {
		return nil
	}

	alert = model.Alert{
		ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindSyncFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: stackName,
		Status: model.AlertStatusOpen, Title: title, Message: details.message,
		FailureStage: details.stage, ErrorCode: details.code,
		DeploymentID: details.deploymentID, Commit: details.commit,
		Occurrences: failures, OpenedAt: now, UpdatedAt: now, OpenEventID: eventID, LatestEventID: eventID,
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

func (s *Subscriber) stackRemoved(stackName string) bool {
	readiness, ok := s.stacks.(interface{ Ready() bool })
	if !ok || !readiness.Ready() {
		return false
	}
	for _, stack := range s.stacks.Stacks() {
		if stack.Name == stackName {
			return false
		}
	}
	return true
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
	fingerprint := model.SyncFailedFingerprint(event.StackName)
	if err := s.store.ResetFailureStreak(ctx, fingerprint); err != nil {
		return fmt.Errorf("reset deployment failure streak: %w", err)
	}
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil {
		if !errors.Is(err, modelstore.ErrAlertNotFound) {
			return fmt.Errorf("find open deployment alert: %w", err)
		}
	} else {
		now := s.now()
		alert.Status = model.AlertStatusResolved
		alert.UpdatedAt = now
		alert.ResolvedAt = &now
		alert.Resolution = &model.AlertResolution{
			Reason:  model.ResolutionReasonRecovered,
			Message: "Synchronization completed successfully",
			EventID: eventReferenceID(eventID, s.newID),
		}
		if err = s.store.Update(ctx, alert); err != nil {
			return fmt.Errorf("resolve deployment alert: %w", err)
		}
		if err = s.publishResolved(ctx, alert); err != nil {
			return err
		}
	}
	return nil
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
		DeploymentID: alert.DeploymentID, ErrorCode: alert.ErrorCode, Occurrences: alert.Occurrences,
		OpenedAt: alert.OpenedAt, ResolvedAt: alert.ResolvedAt}
	if alert.Kind == model.AlertKindGitRepositoryUnavailable && alert.ResolvedAt != nil {
		duration := alert.ResolvedAt.Sub(alert.OpenedAt)
		if duration < 0 {
			duration = 0
		}
		snapshot.Duration = duration.Round(time.Second).String()
	}
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

func isTemporary(err error) bool {
	var classified faults.Error
	return errors.As(err, &classified) && classified.Temporary()
}

func safeFailureMessage(err error, code, stage string) string {
	if diagnostic := faults.SafeDiagnostic(err); diagnostic != "" && diagnostic != code {
		return diagnostic
	}
	if stage == "" {
		stage = "an unknown stage"
	}
	return fmt.Sprintf("Synchronization failed during %s. No detailed diagnostic was recorded.", stage)
}

func safeUnknownOutcomeMessage(err error, code, stage string) string {
	if diagnostic := faults.SafeDiagnostic(err); diagnostic != "" && diagnostic != code {
		return diagnostic
	}
	if stage == "" {
		stage = "an unknown stage"
	}
	return fmt.Sprintf(
		"Deployment outcome is unknown after interruption during %s. No detailed diagnostic was recorded.", stage,
	)
}
