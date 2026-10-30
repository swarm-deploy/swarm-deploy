package deployments

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// ErrInconsistentDeployment means the persisted deployment cannot be retried.
var ErrInconsistentDeployment = errors.New("deployment row and payload are inconsistent")

// Publisher persists deployment domain events in the caller's transaction.
type Publisher interface {
	Publish(context.Context, events.Event) error
}

// InterruptProjector updates a consumer-owned projection for an interrupted deployment.
// It is called synchronously in the deployment creation transaction.
type InterruptProjector func(context.Context, deploymentmodel.Deployment) error

// Service owns deployment and attempt creation.
type Service struct {
	db     storage.Database
	store  *deploymentstore.Store
	events Publisher
	now    func() time.Time
}

// NewService creates a deployment creation service using the shared transaction infrastructure.
func NewService(db storage.Database, publisher Publisher) *Service {
	return &Service{db: db, store: deploymentstore.NewStore(db), events: publisher, now: time.Now}
}

// StartIfChanged creates an attempt before the caller begins external apply.
// Unchanged successful or currently running desired state returns nil. Failed and
// interrupted state receives a new attempt on the existing Deployment.
func (s *Service) StartIfChanged(
	ctx context.Context,
	stack string,
	commit string,
	file compose.File,
	sourceChanged bool,
	projectInterruption InterruptProjector,
) (*deploymentmodel.Attempt, error) {
	desired, err := deploymentmodel.Prepare(file)
	if err != nil {
		return nil, err
	}
	var result *deploymentmodel.Attempt
	err = s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var startErr error
		result, startErr = s.startPrepared(ctx, stack, commit, desired, sourceChanged, projectInterruption)
		return startErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) startPrepared(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
	sourceChanged bool,
	projectInterruption InterruptProjector,
) (*deploymentmodel.Attempt, error) {
	latest, latestDesired, err := s.store.Latest(ctx, stack)
	if errors.Is(err, deploymentstore.ErrNotFound) {
		if !sourceChanged {
			return nil, nil //nolint:nilnil // Nil attempt explicitly means no deployment execution.
		}
		return s.createDeployment(ctx, stack, commit, desired)
	}
	if err != nil {
		return nil, err
	}
	if latestDesired.Digest == desired.Digest {
		return s.startExisting(ctx, latest)
	}
	removedServices := desiredServiceRemovals(latestDesired.Definition, desired.Definition)
	if err = s.supersedePrevious(ctx, latest, projectInterruption); err != nil {
		return nil, err
	}
	attempt, err := s.createDeployment(ctx, stack, commit, desired)
	if err != nil {
		return nil, err
	}
	for _, serviceName := range removedServices {
		if err = s.events.Publish(ctx, &events.ServiceRemovedFromDesired{
			StackName: stack, ServiceName: serviceName, Commit: commit,
		}); err != nil {
			return nil, err
		}
	}
	return attempt, nil
}

func desiredServiceRemovals(previous, current compose.File) []string {
	currentNames := make(map[string]struct{}, len(current.Compose.Services))
	for _, service := range current.Compose.Services {
		currentNames[service.Name] = struct{}{}
	}
	removed := make([]string, 0)
	for _, service := range previous.Compose.Services {
		if _, exists := currentNames[service.Name]; !exists {
			removed = append(removed, service.Name)
		}
	}
	sort.Strings(removed)
	return removed
}

func (s *Service) startExisting(
	ctx context.Context,
	latest deploymentmodel.Deployment,
) (*deploymentmodel.Attempt, error) {
	switch latest.Status {
	case deploymentmodel.Succeeded, deploymentmodel.Running:
		return nil, nil //nolint:nilnil // Nil attempt explicitly means no deployment execution.
	case deploymentmodel.Failed, deploymentmodel.Interrupted:
		attempt := newAttempt(latest.ID, latest.AttemptCount+1, s.now())
		if err := s.store.StartAttempt(ctx, latest, attempt); err != nil {
			return nil, err
		}
		return &attempt, nil
	case deploymentmodel.Superseded:
		return nil, ErrInconsistentDeployment
	default:
		return nil, fmt.Errorf("unsupported deployment status %q", latest.Status)
	}
}

func (s *Service) supersedePrevious(
	ctx context.Context,
	latest deploymentmodel.Deployment,
	projectInterruption InterruptProjector,
) error {
	if latest.Status != deploymentmodel.Running {
		return nil
	}
	attempt, err := s.runningAttempt(ctx, latest)
	if err != nil {
		return err
	}
	return s.InterruptAttempt(ctx, attempt, s.now().UTC(), projectInterruption)
}

func (s *Service) runningAttempt(
	ctx context.Context,
	deployment deploymentmodel.Deployment,
) (deploymentmodel.Attempt, error) {
	if len(deployment.Attempts) > 0 {
		return deployment.Attempts[0], nil
	}
	attempt := newAttempt(deployment.ID, 1, deployment.StartedAt)
	if err := s.store.InsertLegacyAttempt(ctx, attempt); err != nil {
		return deploymentmodel.Attempt{}, err
	}
	return attempt, nil
}

func (s *Service) createDeployment(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
) (*deploymentmodel.Attempt, error) {
	d, attempt, err := s.newDeployment(ctx, stack, commit, desired)
	if err != nil {
		return nil, err
	}
	if err = s.store.CreateDeployment(ctx, d, desired, attempt); err != nil {
		return nil, err
	}
	return &attempt, nil
}

func (s *Service) newDeployment(
	ctx context.Context,
	stack string,
	commit string,
	desired deploymentmodel.Prepared,
) (deploymentmodel.Deployment, deploymentmodel.Attempt, error) {
	basis, basisKind, basisID, err := s.comparisonBasis(ctx, stack)
	if err != nil {
		return deploymentmodel.Deployment{}, deploymentmodel.Attempt{}, err
	}
	changes := deploymentmodel.Compare(basis, desired)
	summary, resources := deploymentmodel.Summarize(changes)
	id := uuid.NewString()
	attempt := newAttempt(id, 1, s.now())
	d := deploymentmodel.Deployment{
		ID: id, Stack: stack, Commit: commit, Status: deploymentmodel.Running, StartedAt: attempt.StartedAt,
		Phase:              deploymentmodel.PhaseApply,
		ApplyStatus:        deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending,
		CleanupStatus:      deploymentmodel.StagePending,
		ActualStateStatus:  deploymentmodel.ActualStateUnknown,
		ComparisonBasis:    basisKind,
		ComparisonStatus:   deploymentmodel.ComparisonUnknown,
		BasisDeploymentID:  basisID,
		Summary:            summary,
		Resources:          resources,
		Changes:            changes,
		DiffAvailable:      true,
		LatestAttemptID:    attempt.ID,
		AttemptCount:       1,
	}
	return d, attempt, nil
}

func newAttempt(deploymentID string, number int, now time.Time) deploymentmodel.Attempt {
	return deploymentmodel.Attempt{
		ID: uuid.NewString(), DeploymentID: deploymentID, Number: number, Status: deploymentmodel.Running,
		StartedAt: now.UTC(), Phase: deploymentmodel.PhaseApply, ApplyStatus: deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending,
		CleanupStatus:      deploymentmodel.StagePending,
		ActualStateStatus:  deploymentmodel.ActualStateUnknown,
	}
}

func (s *Service) comparisonBasis(
	ctx context.Context,
	stack string,
) (deploymentmodel.Prepared, deploymentmodel.ComparisonBasis, string, error) {
	baseline, err := s.store.Baseline(ctx, stack)
	if errors.Is(err, deploymentstore.ErrNotFound) {
		return deploymentmodel.Prepared{}, deploymentmodel.BasisNone, "", nil
	}
	if err != nil {
		return deploymentmodel.Prepared{}, "", "", err
	}
	id, err := s.store.BaselineDeploymentID(ctx, stack)
	return baseline, deploymentmodel.BasisSuccessfulBaseline, id, err
}

// InterruptAttempt persists an interruption and publishes it in the caller's transaction.
// The projection callback, when present, runs after lifecycle persistence and before publication.
func (s *Service) InterruptAttempt(
	ctx context.Context,
	attempt deploymentmodel.Attempt,
	finishedAt time.Time,
	projectInterruption InterruptProjector,
) error {
	finishedAt = finishedAt.UTC()
	d, err := s.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return err
	}
	desired, err := s.store.Desired(ctx, d.ID)
	if err != nil {
		return err
	}
	attempt.Status, attempt.FinishedAt, attempt.ErrorCode = deploymentmodel.Interrupted, &finishedAt, "process_interrupted"
	attempt.Phase, attempt.ApplyStatus = deploymentmodel.PhaseApply, deploymentmodel.StageUnknown
	attempt.VerificationStatus, attempt.CleanupStatus = deploymentmodel.StageSkipped, deploymentmodel.StageSkipped
	attempt.ActualStateStatus, attempt.ObservedAt = deploymentmodel.ActualStateUnknown, nil
	if err = s.store.FinishAttempt(ctx, d, attempt); err != nil {
		return err
	}
	if projectInterruption != nil {
		if err = projectInterruption(ctx, d); err != nil {
			return err
		}
	}
	return s.events.Publish(ctx, &events.DeployInterrupted{
		DeploymentID: d.ID, AttemptID: attempt.ID, StackName: d.Stack, Commit: d.Commit,
		Services: desired.Compose.Services, Reason: "process_interrupted",
	})
}
