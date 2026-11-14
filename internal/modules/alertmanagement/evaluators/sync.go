package evaluators

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

const (
	syncFailureDiagnostic = "Synchronization failed during %s. No detailed diagnostic was recorded."
	interruptedDiagnostic = "Deployment outcome is unknown after interruption during %s. " +
		"No detailed diagnostic was recorded."
)

// SyncEvaluator evaluates failures and recoveries of stack synchronization.
type SyncEvaluator struct {
	store  modelstore.Store
	stacks config.StackProvider
}

// NewSyncEvaluator reads open sync incidents and recognizes removed stacks.
func NewSyncEvaluator(store modelstore.Store, stacks config.StackProvider) *SyncEvaluator {
	return &SyncEvaluator{store: store, stacks: stacks}
}

// Evaluate returns actions for a deployment, preparation or interruption event.
func (e *SyncEvaluator) Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.DeployFailed:
		diagnostic := source.Error
		if diagnostic == nil {
			if logs := strings.TrimSpace(strings.Join(source.Logs, "\n")); logs != "" {
				diagnostic = errors.New(logs)
			}
		}
		return e.failure(ctx, model.Alert{
			ResourceID: source.StackName, Title: "Synchronization failed",
			FailureStage: source.Stage, ErrorCode: source.ErrorCode, DeploymentID: source.DeploymentID,
			Commit: source.Commit, LatestEventID: event.ID,
		}, source.Error, diagnostic, syncFailureDiagnostic)
	case *events.DeployPreparationFailed:
		return e.failure(ctx, model.Alert{
			ResourceID: source.StackName, Title: "Synchronization failed",
			FailureStage: source.Stage, ErrorCode: source.ErrorCode, Commit: source.Commit, LatestEventID: event.ID,
		}, source.Error, source.Error, syncFailureDiagnostic)
	case *events.DeployInterrupted:
		return e.failure(ctx, model.Alert{
			ResourceID: source.StackName, Title: "Synchronization outcome unknown",
			FailureStage: source.Stage, ErrorCode: source.Reason, DeploymentID: source.DeploymentID,
			Commit: source.Commit, LatestEventID: event.ID,
		}, source.Error, source.Error, interruptedDiagnostic)
	case *events.DeploySuccess:
		fingerprint := model.SyncFailedFingerprint(source.StackName)
		return []model.AlertAction{
			model.NewResetFailureStreakAction(fingerprint),
			model.NewResolveAction(fingerprint, event.ID,
				model.ResolutionReasonRecovered, "Synchronization completed successfully"),
		}, nil
	default:
		return nil, nil
	}
}

func (e *SyncEvaluator) failure(
	ctx context.Context, incoming model.Alert, cause, diagnostic error, fallbackFormat string,
) ([]model.AlertAction, error) {
	incoming.Fingerprint = model.SyncFailedFingerprint(incoming.ResourceID)
	if e.stackRemoved(incoming.ResourceID) {
		return []model.AlertAction{model.NewResetFailureStreakAction(incoming.Fingerprint)}, nil
	}

	incoming.Kind = model.AlertKindSyncFailed
	incoming.ResourceType = model.ResourceTypeStack
	alert, err := alertForFailure(ctx, e.store, incoming)
	if err != nil {
		return nil, err
	}
	alert.Title, alert.FailureStage, alert.ErrorCode = incoming.Title, incoming.FailureStage, incoming.ErrorCode
	alert.DeploymentID, alert.Commit, alert.LatestEventID = incoming.DeploymentID, incoming.Commit, incoming.LatestEventID
	if message := faults.SafeDiagnostic(diagnostic); message != "" && message != incoming.ErrorCode {
		alert.Message = message
	} else if alert.Message == "" {
		stage := incoming.FailureStage
		if stage == "" {
			stage = "an unknown stage"
		}
		alert.Message = fmt.Sprintf(fallbackFormat, stage)
	}

	openAfter := openImmediately
	var classified faults.Error
	if errors.As(cause, &classified) && classified.Temporary() {
		openAfter = temporaryFailureThreshold
	}
	return []model.AlertAction{model.NewRecordFailureAction(alert, openAfter, true, failureFindings(cause)...)}, nil
}

func failureFindings(cause error) []model.Finding {
	var coded faults.Coded
	if errors.As(cause, &coded) {
		if code := coded.FaultCode(); code != "" {
			return []model.Finding{{Code: code, Summary: coded.FaultSummary()}}
		}
	}
	return nil
}

func (e *SyncEvaluator) stackRemoved(stackName string) bool {
	readiness, ok := e.stacks.(interface{ Ready() bool })
	if !ok || !readiness.Ready() {
		return false
	}
	for _, stack := range e.stacks.Stacks() {
		if stack.Name == stackName {
			return false
		}
	}
	return true
}
