package deployments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	initJobPersistenceTimeout = 2 * time.Second
	maxInitJobDiagnosticLines = 20
	maxInitJobDiagnosticBytes = 1600
)

// Executor is the synchronous public deployment execution boundary.
type Executor interface {
	// Execute runs and completes one stack deployment when the desired state needs an Attempt.
	Execute(context.Context, ExecuteRequest) (ExecuteResult, error)
	// ExecuteService runs one service remediation without creating a stack Deployment.
	ExecuteService(context.Context, ExecuteServiceRequest) (deployer.Result, error)
	// Recover interrupts unfinished Attempts left by a previous process.
	Recover(context.Context) error
}

// ServicePruner removes services absent from a desired stack state.
type ServicePruner interface {
	// Prune removes eligible orphaned services and returns their names.
	Prune(context.Context, pruner.PruneServicesRequest) ([]string, error)
}

// ExecuteRequest contains the effective desired state and execution inputs prepared by GitOps.
type ExecuteRequest struct {
	// Stack contains stack identity and prune policy overrides.
	Stack config.StackSpec
	// Commit identifies the source revision that requested execution.
	Commit string
	// Desired is the effective desired state persisted for comparison and history.
	Desired compose.File
	// DeployComposePath identifies the prepared Compose artifact retained for tracing and compatibility.
	DeployComposePath string
	// ManualRetry explicitly retries a failed desired state, bypassing automatic backoff.
	ManualRetry bool
	// SourceChanged reports whether GitOps observed a new source digest.
	SourceChanged bool
}

// ExecuteServiceRequest contains one drift-remediation execution.
type ExecuteServiceRequest struct {
	// StackName identifies the owning stack.
	StackName string
	// SourceComposePath is the source Compose path used for resource resolution.
	SourceComposePath string
	// ServiceName identifies the service to apply.
	ServiceName string
	// Desired contains the effective desired stack.
	Desired compose.Compose
	// Deploy contains the rendered stack used for Docker apply.
	Deploy compose.Compose
}

// ExecuteResult contains observations produced by a synchronous stack execution.
type ExecuteResult struct {
	// Attempt is nil when the desired state did not require a new execution.
	Attempt *deploymentmodel.Attempt
	// RetryDeferred reports that failure/backoff prevented a stack deployment retry.
	RetryDeferred bool
	// UnresolvedServices must not be automatically retried by independent self-heal.
	UnresolvedServices []string
	// LiveServices is the post-apply stack observation.
	LiveServices []swarm.StackService
	// ObservedAt is set after live state was read successfully.
	ObservedAt time.Time
	// PrunedServices contains orphaned services removed during cleanup.
	PrunedServices []string
	// Status is the terminal lifecycle status when an Attempt was executed.
	Status deploymentmodel.Status
	// ErrorCode is the stable terminal failure category.
	ErrorCode string
}

type pendingCompletion struct {
	attemptID  string
	completion Completion
}

// Controller owns synchronous deployment execution and lifecycle orchestration.
type Controller struct {
	lifecycle *Service
	deployer  deployer.StackDeployer
	services  swarm.ServiceManager
	pruner    ServicePruner
	metrics   metrics.Deploys

	pendingMu sync.Mutex
	pending   map[string]pendingCompletion
}

// NewController creates the synchronous deployment execution boundary.
func NewController(
	lifecycle *Service,
	stackDeployer deployer.StackDeployer,
	services swarm.ServiceManager,
	servicePruner ServicePruner,
	deployMetrics metrics.Deploys,
) *Controller {
	return &Controller{
		lifecycle: lifecycle, deployer: stackDeployer, services: services, pruner: servicePruner,
		metrics: deployMetrics, pending: map[string]pendingCompletion{},
	}
}

// Execute runs the complete synchronous lifecycle for one prepared desired state.
func (c *Controller) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if err := c.recoverPending(ctx, req.Stack.Name); err != nil {
		return ExecuteResult{}, fmt.Errorf("recover deployment completion: %w", err)
	}
	attempt, err := c.lifecycle.StartIfChanged(
		ctx, req.Stack.Name, req.Commit, req.Desired, req.SourceChanged, StartOptions{ManualRetry: req.ManualRetry},
	)
	result := ExecuteResult{Attempt: attempt}
	if err != nil {
		return result, err
	}
	if attempt == nil {
		latest, _, latestErr := c.lifecycle.store.Latest(ctx, req.Stack.Name)
		if latestErr != nil && !errors.Is(latestErr, deploymentstore.ErrNotFound) {
			return result, latestErr
		}
		result.Status, result.ErrorCode = latest.Status, latest.ErrorCode
		result.RetryDeferred = latest.Status == deploymentmodel.Failed || latest.Status == deploymentmodel.Interrupted
		if result.RetryDeferred {
			result.UnresolvedServices = unresolvedServices(latest, req.Desired)
		}
		return result, nil
	}

	options, err := c.stackOptions(ctx, *attempt)
	if err != nil {
		return result, err
	}
	deployResult, deployErr := c.deployer.DeployStack(
		ctx, req.Stack.Name, req.Desired.Path, req.DeployComposePath, req.Desired.Compose, options...,
	)
	c.persistInitJobs(ctx, attempt.ID, deployResult.InitJobs)
	if deployErr != nil {
		completion := classifyExecutionFailure(deployErr)
		completion.UnresolvedServices = deployResult.UnresolvedServices
		result.Status, result.ErrorCode = completionStatus(completion), completion.ErrorCode()
		completeErr := c.complete(ctx, req.Stack.Name, attempt.ID, completion)
		c.record(req, "failed")
		return result, errors.Join(deployErr, completeErr)
	}

	live, observeErr := c.services.ListServices(ctx, swarm.ListServicesFilter{Stack: req.Stack.Name})
	if observeErr != nil {
		completion := Completion{
			Outcome: CompletionVerificationInterrupted, Diagnostic: observeErr,
			UnresolvedServices: deployResult.UnresolvedServices,
		}
		result.Status, result.ErrorCode = deploymentmodel.Interrupted, completion.ErrorCode()
		completeErr := c.complete(ctx, req.Stack.Name, attempt.ID, completion)
		c.record(req, "failed")
		return result, errors.Join(observeErr, completeErr)
	}
	result.LiveServices = live
	result.ObservedAt = time.Now().UTC()

	pruned, cleanupErr := c.pruner.Prune(ctx, pruner.PruneServicesRequest{
		Stack: req.Stack, Commit: req.Commit, Desired: req.Desired.Compose.Services, Live: live,
	})
	result.PrunedServices = pruned
	if cleanupErr != nil {
		completion := Completion{
			Outcome: CompletionCleanupFailed, Diagnostic: cleanupErr, ObservedAt: &result.ObservedAt,
			UnresolvedServices: deployResult.UnresolvedServices,
		}
		result.Status, result.ErrorCode = deploymentmodel.Failed, completion.ErrorCode()
		completeErr := c.complete(ctx, req.Stack.Name, attempt.ID, completion)
		c.record(req, "failed")
		return result, errors.Join(cleanupErr, completeErr)
	}

	completion := Completion{Outcome: CompletionSucceeded, ObservedAt: &result.ObservedAt}
	result.Status = deploymentmodel.Succeeded
	if err = c.complete(ctx, req.Stack.Name, attempt.ID, completion); err != nil {
		return result, err
	}
	c.record(req, "success")
	return result, nil
}

// ExecuteService runs one synchronous service remediation through the owned Deployer.
func (c *Controller) ExecuteService(ctx context.Context, req ExecuteServiceRequest) (deployer.Result, error) {
	return c.deployer.DeployService(
		ctx, req.StackName, req.SourceComposePath, req.ServiceName, req.Desired, req.Deploy,
	)
}

// Recover interrupts Attempts that were still running when the previous process stopped.
func (c *Controller) Recover(ctx context.Context) error { return c.lifecycle.InterruptRunning(ctx) }

func (c *Controller) complete(
	ctx context.Context,
	stack string,
	attemptID string,
	completion Completion,
) error {
	err := c.lifecycle.Complete(ctx, attemptID, completion)
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if err != nil {
		c.pending[stack] = pendingCompletion{attemptID: attemptID, completion: completion}
		return err
	}
	delete(c.pending, stack)
	return nil
}

func (c *Controller) recoverPending(ctx context.Context, stack string) error {
	c.pendingMu.Lock()
	pending, ok := c.pending[stack]
	c.pendingMu.Unlock()
	if !ok {
		return nil
	}
	err := c.lifecycle.Complete(ctx, pending.attemptID, pending.completion)
	if err == nil {
		c.pendingMu.Lock()
		delete(c.pending, stack)
		c.pendingMu.Unlock()
		return nil
	}
	if !errors.Is(err, deploymentstore.ErrNotRunning) {
		return err
	}
	actual, inspectErr := c.lifecycle.Inspect(ctx, pending.attemptID)
	if inspectErr != nil {
		return fmt.Errorf("inspect deployment completion conflict: %w", inspectErr)
	}
	if actual.Stack != stack {
		return fmt.Errorf("deployment completion conflict stack mismatch: expected %s, actual %s", stack, actual.Stack)
	}
	if actual.Status == deploymentmodel.Running {
		return fmt.Errorf("completion returned not running but attempt %s is running: %w", pending.attemptID, err)
	}
	slog.WarnContext(ctx, "deployment completion lifecycle conflict",
		slog.String("attempt_id", pending.attemptID), slog.String("stack", stack),
		slog.String("actual_status", string(actual.Status)))
	c.pendingMu.Lock()
	delete(c.pending, stack)
	c.pendingMu.Unlock()
	return nil
}

func (c *Controller) persistInitJobs(ctx context.Context, attemptID string, results []deployer.InitJobResult) {
	for index, result := range results {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initJobPersistenceTimeout)
		err := c.lifecycle.SaveInitJobExecution(persistCtx, deploymentmodel.InitJobExecution{
			AttemptID: attemptID, Ordinal: index + 1, ServiceName: result.ServiceName, JobName: result.JobName,
			Status: deploymentmodel.InitJobStatus(result.Status), StartedAt: result.StartedAt,
			FinishedAt: result.FinishedAt, Logs: result.Logs, LogsAvailable: result.LogsAvailable,
			LogsTruncated: result.LogsTruncated,
		})
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "persist init job execution", slog.String("attempt_id", attemptID),
				slog.String("service_name", result.ServiceName), slog.String("job_name", result.JobName),
				slog.Any("err", err))
		}
	}
}

func (c *Controller) record(req ExecuteRequest, status string) {
	for _, service := range req.Desired.Compose.Services {
		c.metrics.RecordDeploy(req.Stack.Name, service.Name, status)
	}
}

func classifyExecutionFailure(err error) Completion {
	if deployer.ConvergenceFailed(err) {
		if errors.Is(err, waiter.ErrConvergenceFailed) {
			return Completion{Outcome: CompletionConvergenceFailed, Diagnostic: err}
		}
		return Completion{Outcome: CompletionVerificationInterrupted, Diagnostic: err}
	}
	if deployer.InitJobsFailed(err) {
		diagnostic, _ := withInitJobLogs(err)
		return Completion{Outcome: CompletionInitJobFailed, Diagnostic: diagnostic}
	}
	return Completion{Outcome: CompletionApplyFailed, Diagnostic: err}
}

func completionStatus(completion Completion) deploymentmodel.Status {
	if completion.Outcome == CompletionVerificationInterrupted || completion.Outcome == CompletionProcessInterrupted {
		return deploymentmodel.Interrupted
	}
	return deploymentmodel.Failed
}

func withInitJobLogs(err error) (error, bool) {
	var logged interface{ Logs() []string }
	if !errors.As(err, &logged) {
		return err, false
	}
	lines := initJobDiagnosticLines(logged.Logs())
	if len(lines) == 0 {
		return err, true
	}
	return fmt.Errorf("%w\ninit job logs:\n%s", err, strings.Join(lines, "\n")), true
}

func initJobDiagnosticLines(logs []string) []string {
	var lines []string
	used := 0
	for _, raw := range logs {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || mayContainCredentials(line) {
				continue
			}
			line = faults.SafeDiagnostic(errors.New(line))
			remaining := maxInitJobDiagnosticBytes - used - len(lines)
			if line == "" || remaining <= 0 || len(lines) >= maxInitJobDiagnosticLines {
				break
			}
			if len(line) > remaining {
				line = line[:remaining]
			}
			lines = append(lines, line)
			used += len(line)
		}
		if used+len(lines) >= maxInitJobDiagnosticBytes || len(lines) >= maxInitJobDiagnosticLines {
			break
		}
	}
	return lines
}

func mayContainCredentials(line string) bool {
	value := strings.ToLower(line)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "credential", "authorization", "bearer",
		"private_key", "api_key", "apikey", "dsn", "database_url", "postgres://", "postgresql://",
		"://", "-----begin", "=",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func (c *Controller) stackOptions(
	ctx context.Context, attempt deploymentmodel.Attempt,
) ([]deployer.StackOptions, error) {
	current, err := c.lifecycle.store.Get(ctx, attempt.DeploymentID)
	if err != nil {
		return nil, err
	}
	if attempt.Number == 1 && len(current.UnresolvedServices) == 0 {
		return nil, nil
	}
	return []deployer.StackOptions{{
		RetryFailedUpdates: attempt.Number > 1,
		UnresolvedServices: current.UnresolvedServices,
	}}, nil
}
