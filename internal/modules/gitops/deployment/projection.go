package deployment

import (
	"context"
	"fmt"
	"time"

	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ProjectionSubscriber maintains GitOps runtime state from durable deployment completion events.
type ProjectionSubscriber struct {
	db          storage.Database
	deployments *deploymentstore.Store
	runtime     *modelstore.SQLStore
}

// NewProjectionSubscriber creates the GitOps deployment completion projection.
func NewProjectionSubscriber(
	db storage.Database,
	deployments *deploymentstore.Store,
	runtime *modelstore.SQLStore,
) *ProjectionSubscriber {
	return &ProjectionSubscriber{db: db, deployments: deployments, runtime: runtime}
}

// Handle idempotently projects the latest deployment Attempt into GitOps runtime state.
func (s *ProjectionSubscriber) Handle(ctx context.Context, envelope events.Envelope) error {
	ref, status, errorCode, ok := projectionEvent(envelope.Event)
	if !ok {
		return nil
	}
	if ref.DeploymentID == "" || ref.AttemptID == "" {
		return fmt.Errorf("deployment completion reference missing")
	}
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.project(ctx, ref, status, errorCode)
	})
}

func (s *ProjectionSubscriber) project(
	ctx context.Context,
	ref events.DeployEvent,
	status deploymentmodel.Status,
	errorCode string,
) error {
	deployment, err := s.deployments.Get(ctx, ref.DeploymentID)
	if err != nil {
		return err
	}
	attempt, err := s.deployments.GetAttempt(ctx, ref.AttemptID)
	if err != nil {
		return err
	}
	latest, desired, err := s.deployments.Latest(ctx, deployment.Stack)
	if err != nil {
		return err
	}
	if latest.ID != deployment.ID || latest.LatestAttemptID != attempt.ID {
		return nil
	}
	return s.runtime.Update(ctx, func(runtime *gitopsmodel.Runtime) {
		projectRuntime(runtime, deployment, attempt, desired, status, errorCode)
	})
}

func projectRuntime(
	runtime *gitopsmodel.Runtime,
	deployment deploymentmodel.Deployment,
	attempt deploymentmodel.Attempt,
	desired deploymentmodel.Prepared,
	status deploymentmodel.Status,
	errorCode string,
) {
	state := runtime.Stacks[deployment.Stack]
	state.SourceDigest = desired.Digest
	state.LastCommit = deployment.Commit
	state.LastError = errorCode
	state.LastDeployAt = attempt.StartedAt
	if state.Services == nil {
		state.Services = map[string]gitopsmodel.Service{}
	}
	for _, service := range desired.Definition.Compose.Services {
		current, exists := state.Services[service.Name]
		if exists && status == deploymentmodel.Succeeded {
			continue
		}
		current.Image = service.Image
		current.SyncAt = projectionTime(attempt)
		current.SyncError = errorCode
		current.SyncStatus = gitopsmodel.SyncStatusOutOfSync
		if status == deploymentmodel.Succeeded {
			current.SyncStatus = gitopsmodel.SyncStatusSynced
			current.SyncError = ""
		}
		state.Services[service.Name] = current
	}
	state.Status = gitopsmodel.NewStackStatus(state.Services)
	runtime.Stacks[deployment.Stack] = state
}

func projectionEvent(event events.Event) (events.DeployEvent, deploymentmodel.Status, string, bool) {
	switch item := event.(type) {
	case *events.DeploySuccess:
		return item.DeployEvent, deploymentmodel.Succeeded, "", true
	case *events.DeployFailed:
		return item.DeployEvent, deploymentmodel.Failed, item.ErrorCode, true
	case *events.DeployInterrupted:
		return events.DeployEvent{
			DeploymentID: item.DeploymentID, AttemptID: item.AttemptID,
			StackName: item.StackName, Commit: item.Commit,
		}, deploymentmodel.Interrupted, item.Reason, true
	default:
		return events.DeployEvent{}, "", "", false
	}
}

func projectionTime(attempt deploymentmodel.Attempt) time.Time {
	if attempt.FinishedAt != nil {
		return *attempt.FinishedAt
	}
	return attempt.StartedAt
}
