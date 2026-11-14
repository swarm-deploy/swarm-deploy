package evaluators

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

const interruptedTitle = "Synchronization outcome unknown"

// SyncEvaluator evaluates failures and recoveries of stack synchronization.
type SyncEvaluator struct {
	stacks config.StackProvider
}

// NewSyncEvaluator uses the desired stack configuration to recognize removed stacks.
func NewSyncEvaluator(stacks config.StackProvider) *SyncEvaluator {
	return &SyncEvaluator{stacks: stacks}
}

// Evaluate returns actions for a deployment, preparation or interruption event.
func (e *SyncEvaluator) Evaluate(_ context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.DeployFailed:
		diagnostic := source.Error
		if diagnostic == nil {
			if logs := strings.TrimSpace(strings.Join(source.Logs, "\n")); logs != "" {
				diagnostic = errors.New(logs)
			}
		}
		return e.failure(model.Alert{
			ResourceID: source.StackName, Title: "Synchronization failed",
			FailureStage: source.Stage, ErrorCode: source.ErrorCode, DeploymentID: source.DeploymentID,
			Commit: source.Commit, LatestEventID: event.ID,
		}, source.Error, diagnostic), nil
	case *events.DeployPreparationFailed:
		return e.failure(model.Alert{
			ResourceID: source.StackName, Title: "Synchronization failed",
			FailureStage: source.Stage, ErrorCode: source.ErrorCode, Commit: source.Commit, LatestEventID: event.ID,
		}, source.Error, source.Error), nil
	case *events.DeployInterrupted:
		return e.failure(model.Alert{
			ResourceID: source.StackName, Title: interruptedTitle,
			FailureStage: source.Stage, ErrorCode: source.Reason, DeploymentID: source.DeploymentID,
			Commit: source.Commit, LatestEventID: event.ID,
		}, source.Error, source.Error), nil
	case *events.DeploySuccess:
		fingerprint := model.SyncFailedFingerprint(source.StackName)
		return []model.AlertAction{
			resetFailureStreak(fingerprint),
			resolve(fingerprint, event.ID, model.ResolutionReasonRecovered, "Synchronization completed successfully"),
		}, nil
	default:
		return nil, nil
	}
}

func (e *SyncEvaluator) failure(alert model.Alert, cause, diagnostic error) []model.AlertAction {
	alert.Fingerprint = model.SyncFailedFingerprint(alert.ResourceID)
	if e.stackRemoved(alert.ResourceID) {
		return []model.AlertAction{resetFailureStreak(alert.Fingerprint)}
	}

	alert.Kind = model.AlertKindSyncFailed
	alert.ResourceType = model.ResourceTypeStack
	alert.Message = faults.SafeDiagnostic(diagnostic)
	hasDiagnostic := alert.Message != "" && alert.Message != alert.ErrorCode
	if !hasDiagnostic {
		stage := alert.FailureStage
		if stage == "" {
			stage = "an unknown stage"
		}
		if alert.Title == interruptedTitle {
			alert.Message = fmt.Sprintf(
				"Deployment outcome is unknown after interruption during %s. No detailed diagnostic was recorded.", stage,
			)
		} else {
			alert.Message = fmt.Sprintf("Synchronization failed during %s. No detailed diagnostic was recorded.", stage)
		}
	}

	openAfter := openImmediately
	var classified faults.Error
	if errors.As(cause, &classified) && classified.Temporary() {
		openAfter = temporaryFailureThreshold
	}
	var coded faults.Coded
	if errors.As(cause, &coded) {
		if code := coded.FaultCode(); code != "" {
			alert.Findings = []model.Finding{{Code: code, Summary: coded.FaultSummary()}}
		}
	}
	return []model.AlertAction{{
		Kind: model.RecordFailure, Alert: alert, OpenAfter: openAfter, TrackFailureStreak: true,
		PreserveExistingMessage: !hasDiagnostic,
	}}
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
