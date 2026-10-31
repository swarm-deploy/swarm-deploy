package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
)

// DeploymentQueue serializes stack executions independently of Git synchronization.
// Pending work is intentionally in memory: reconciliation recreates it after restart.
type DeploymentQueue struct {
	reconciler stackloop.StackReconciler
	snapshots  string

	mu      sync.Mutex
	pending map[string]deploymentJob
	order   []string
	running map[string]string
	stopped bool
	wake    chan struct{}
	stop    chan struct{}
}

type deploymentJob struct {
	request stackloop.ReconciliationRequest
	folder  string
}

// NewDeploymentQueue creates one worker queue without starting any goroutines.
func NewDeploymentQueue(reconciler stackloop.StackReconciler, dataDir string) *DeploymentQueue {
	return &DeploymentQueue{
		reconciler: reconciler,
		snapshots:  filepath.Join(dataDir, "deployment-snapshots"),
		pending:    make(map[string]deploymentJob),
		running:    make(map[string]string),
		wake:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
	}
}

// CleanupStale removes snapshots left by a previous owner. Call only after acquiring ownership.
func (q *DeploymentQueue) CleanupStale() error {
	return os.RemoveAll(q.snapshots)
}

// Enqueue captures an immutable checkout before a subsequent Git pull can mutate files.
// A newer pending revision replaces older pending work for the same stack.
func (q *DeploymentQueue) Enqueue(ctx context.Context, req stackloop.ReconciliationRequest, sourceDir string) error {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return errors.New("deployment queue stopped")
	}
	// Do not deduplicate by commit SHA: referenced files may change at the same revision.
	q.mu.Unlock()

	folder, err := snapshotCheckout(ctx, sourceDir, q.snapshots)
	if err != nil {
		return fmt.Errorf("capture stack %s source snapshot: %w", req.Stack.Name, err)
	}
	req.SourceDirectory = folder
	req.OriginalSourceDirectory = sourceDir
	job := deploymentJob{request: req, folder: folder}

	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		_ = os.RemoveAll(folder)
		return errors.New("deployment queue stopped")
	}
	previous, exists := q.pending[req.Stack.Name]
	if !exists {
		q.order = append(q.order, req.Stack.Name)
	}
	q.pending[req.Stack.Name] = job
	select {
	case q.wake <- struct{}{}:
	default:
	}
	q.mu.Unlock()

	if exists {
		if removeErr := os.RemoveAll(previous.folder); removeErr != nil {
			slog.WarnContext(ctx, "remove superseded deployment snapshot", "err", removeErr)
		}
	}
	return nil
}

// Run executes queued stacks in one worker, independently of the GitOps sync worker.
func (q *DeploymentQueue) Run(ctx context.Context) {
	for {
		job, ok := q.take()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-q.stop:
				return
			case <-q.wake:
				continue
			}
		}
		if err := q.reconciler.Reconcile(ctx, job.request); err != nil {
			slog.ErrorContext(ctx, "queued stack reconcile failed",
				"stack", job.request.Stack.Name, "commit", job.request.Commit, "err", err)
		}
		if err := os.RemoveAll(job.folder); err != nil {
			slog.WarnContext(ctx, "remove completed deployment snapshot", "err", err)
		}
		q.mu.Lock()
		delete(q.running, job.request.Stack.Name)
		q.mu.Unlock()
	}
}

func (q *DeploymentQueue) take() (deploymentJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped || len(q.order) == 0 {
		return deploymentJob{}, false
	}
	stackName := q.order[0]
	q.order = q.order[1:]
	job := q.pending[stackName]
	delete(q.pending, stackName)
	q.running[stackName] = job.request.Commit
	return job, true
}

// Stop rejects new work, drops pending work, and lets the active job finish under its context.
func (q *DeploymentQueue) Stop() {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return
	}
	q.stopped = true
	pending := q.pending
	q.pending = make(map[string]deploymentJob)
	q.order = nil
	close(q.stop)
	q.mu.Unlock()
	for _, job := range pending {
		if err := os.RemoveAll(job.folder); err != nil {
			slog.Warn("remove pending deployment snapshot", "err", err)
		}
	}
}
