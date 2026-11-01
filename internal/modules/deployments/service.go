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

// Service owns deployment and attempt lifecycle persistence.
type Service struct {
	db     storage.Database
	store  *deploymentstore.Store
	events Publisher
	now    func() time.Time
}

// ServiceOption configures a deployment service.
type ServiceOption func(*Service)

// WithClock overrides the deployment service clock.
func WithClock(now func() time.Time) ServiceOption {
	return func(service *Service) { service.now = now }
}

// NewService creates a deployment creation service using the shared transaction infrastructure.
func NewService(db storage.Database, publisher Publisher, options ...ServiceOption) *Service {
	service := &Service{db: db, store: deploymentstore.NewStore(db), events: publisher, now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

const (
	automaticRetryDelay    = time.Minute
	maximumRetryDelay      = time.Hour
)

// StartOptions controls an explicit retry of a persisted unsuccessful deployment.
type StartOptions struct {
	// ManualRetry bypasses failure classification and automatic retry backoff.
	ManualRetry bool
}

// StartIfChanged creates an attempt before the caller begins external apply.
// Unchanged non-retryable failures are suppressed. Transient failures are retried
// indefinitely with capped exponential backoff. Manual retries bypass both.
func (s *Service) StartIfChanged(
	ctx context.Context,
	stack string,
	commit string,
	file compose.File,
	sourceChanged bool,
	options ...StartOptions,
) (*deploymentmodel.Attempt, error) {
	desired, err := deploymentmodel.Prepare(file)
	if err != nil {
		return nil, err
	}
	var result *deploymentmodel.Attempt
	err = s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var startErr error
		manualRetry := len(options) > 0 && options[0].ManualRetry
		result, startErr = s.startPrepared(ctx, stack, commit, desired, sourceChanged, manualRetry)
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
	sourceChanged, manualRetry bool,
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
		return s.startExisting(ctx, latest, manualRetry)
	}
	removedServices := desiredServiceRemovals(latestDesired.Definition, desired.Definition)
	if err = s.supersedePrevious(ctx, latest); err != nil {
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
	manualRetry bool,
) (*deploymentmodel.Attempt, error) {
	switch latest.Status {
	case deploymentmodel.Succeeded, deploymentmodel.Running:
		return nil, nil //nolint:nilnil // Nil attempt explicitly means no deployment execution.
	case deploymentmodel.Failed, deploymentmodel.Interrupted:
		if !manualRetry && !s.automaticRetryAllowed(latest) {
			return nil, nil //nolint:nilnil // A suppressed retry does not execute or publish completion events.
		}
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

// automaticRetryAllowed relies only on persisted details and caps delay without
// capping attempt count or overflowing the duration calculation.
func (s *Service) automaticRetryAllowed(latest deploymentmodel.Deployment) bool {
	if !latest.Retryable || latest.FinishedAt == nil {
		return false
	}
	delay := automaticRetryDelay
	for attempt := 1; attempt < latest.AttemptCount && delay < maximumRetryDelay; attempt++ {
		delay = min(delay*2, maximumRetryDelay)
	}
	return !s.now().Before(latest.FinishedAt.Add(delay))
}

func (s *Service) supersedePrevious(
	ctx context.Context,
	latest deploymentmodel.Deployment,
) error {
	if latest.Status != deploymentmodel.Running {
		return nil
	}
	attempt, err := s.runningAttempt(ctx, latest)
	if err != nil {
		return err
	}
	return s.Complete(ctx, attempt.ID, Completion{Outcome: CompletionProcessInterrupted})
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

// SaveInitJobExecution stores one observational Init Job execution result.
func (s *Service) SaveInitJobExecution(ctx context.Context, execution deploymentmodel.InitJobExecution) error {
	return s.store.SaveInitJobExecution(ctx, execution)
}

// Inspect reads one attempt and its owning stack in a consistent transaction.
func (s *Service) Inspect(ctx context.Context, id string) (deploymentmodel.Attempt, error) {
	var result deploymentmodel.Attempt
	err := s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.store.GetAttempt(ctx, id)
		if err != nil {
			return err
		}
		deployment, err := s.store.Get(ctx, result.DeploymentID)
		if err == nil {
			result.Stack = deployment.Stack
		}
		return err
	})
	return result, err
}

// InterruptRunning recovers unfinished attempts without inventing successful completion.
func (s *Service) InterruptRunning(ctx context.Context) error {
	return s.db.WithinTransaction(ctx, func(ctx context.Context) error {
		return s.interruptRunning(ctx)
	})
}

func (s *Service) interruptRunning(ctx context.Context) error {
	running, err := s.store.RunningAttempts(ctx, "")
	if err != nil {
		return err
	}
	legacy, err := s.store.LegacyRunning(ctx, "")
	if err != nil {
		return err
	}
	for _, deployment := range legacy {
		attempt, materializeErr := s.runningAttempt(ctx, deployment)
		if materializeErr != nil {
			return materializeErr
		}
		running = append(running, attempt)
	}
	return s.interruptAttempts(ctx, running)
}

func (s *Service) interruptAttempts(ctx context.Context, attempts []deploymentmodel.Attempt) error {
	state, err := (Completion{Outcome: CompletionProcessInterrupted}).state()
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		synchronized, syncErr := s.store.SynchronizeRunningAttempt(ctx, attempt)
		if syncErr != nil {
			return syncErr
		}
		if !synchronized {
			if err = s.complete(ctx, attempt.ID, state); err != nil {
				return err
			}
		}
	}
	return nil
}
