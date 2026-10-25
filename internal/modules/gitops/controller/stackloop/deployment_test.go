package stackloop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileDeploymentBoundaries(t *testing.T) {
	for _, scenario := range []string{"success and effective no-op", "prepare failure", "success commit failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			db, state := newSQLState(t)
			bus := outbox.New(db)
			hist, err := history.NewSQLStore(db, 50)
			require.NoError(t, err)
			require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", hist))
			repo := deploymentstore.NewStore(db)
			ctrl := gomock.NewController(t)
			git := gitx.NewMockRepository(ctrl)
			docker := deployer.NewMockStackDeployer(ctrl)
			services := swarm.NewMockServiceManager(ctrl)
			dir := t.TempDir()
			git.EXPECT().WorkingDir().Return(dir).AnyTimes()
			cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data")}}
			r := New(cfg, git, docker, &swarm.Swarm{Services: services, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl)}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)
			req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "first"}
			if scenario == "prepare failure" {
				require.Error(t, r.Reconcile(ctx, req))
				attempts, err := repo.List(ctx, deploymentmodel.ListFilter{})
				require.NoError(t, err)
				assert.Empty(t, attempts.Deployments)
				return
			}
			require.NoError(t, writeComposeFile(dir))
			docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ string, _ string, _ string, _ compose.Compose) (deployer.Result, error) {
					attempts, err := repo.List(ctx, deploymentmodel.ListFilter{})
					require.NoError(t, err)
					require.Len(t, attempts.Deployments, 1)
					assert.Equal(t, deploymentmodel.Running, attempts.Deployments[0].Status, "running must be committed before apply")
					if scenario == "success commit failure" {
						_, err = db.Exec(ctx, "CREATE TRIGGER fail_event BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT,'injected'); END")
						require.NoError(t, err)
					}
					return deployer.Result{}, nil
				})
			services.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil).AnyTimes()
			err = r.Reconcile(ctx, req)
			if scenario == "success commit failure" {
				require.Error(t, err)
				attempts, err := repo.List(ctx, deploymentmodel.ListFilter{})
				require.NoError(t, err)
				require.Len(t, attempts.Deployments, 1)
				assert.Equal(t, deploymentmodel.Running, attempts.Deployments[0].Status)
				_, err = repo.Baseline(ctx, "app")
				require.ErrorIs(t, err, deploymentstore.ErrNotFound)
				_, err = db.Exec(ctx, "DROP TRIGGER fail_event")
				require.NoError(t, err)
				req.Commit = "second"
				require.NoError(t, r.Reconcile(ctx, req))
				recovered, err := repo.Get(ctx, attempts.Deployments[0].ID)
				require.NoError(t, err)
				assert.Equal(t, deploymentmodel.Succeeded, recovered.Status)
				baseline, err := repo.Baseline(ctx, "app")
				require.NoError(t, err)
				assert.Equal(t, "nginx:latest", baseline.Definition.Compose.Services[0].Image)
				return
			}
			require.NoError(t, err)
			rows, err := hist.Read(ctx)
			require.NoError(t, err)
			assert.Empty(t, rows, "history is asynchronous")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("# formatting change only\nservices:\n  api:\n    image: nginx:latest\n"), 0600))
			req.Commit = "second"
			require.NoError(t, r.Reconcile(ctx, req))
			attempts, err := repo.List(ctx, deploymentmodel.ListFilter{})
			require.NoError(t, err)
			require.Len(t, attempts.Deployments, 1)
			assert.Equal(t, deploymentmodel.Succeeded, attempts.Deployments[0].Status)
			assert.Equal(t, "first", attempts.Deployments[0].Commit)
		})
	}
}

func TestReconcileRecoversFailedResultPersistenceInSameProcess(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := outbox.New(db)
	hist, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployFailed, "history", hist))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", hist))
	repo := deploymentstore.NewStore(db)
	ctrl := gomock.NewController(t)
	git := gitx.NewMockRepository(ctrl)
	docker := deployer.NewMockStackDeployer(ctrl)
	services := swarm.NewMockServiceManager(ctrl)
	dir := t.TempDir()
	git.EXPECT().WorkingDir().Return(dir).AnyTimes()
	require.NoError(t, writeComposeFile(dir))
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data")}}
	r := New(cfg, git, docker, &swarm.Swarm{
		Services: services, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
	}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)
	docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ string, _ string, _ string, _ compose.Compose) (deployer.Result, error) {
			_, err := db.Exec(ctx,
				"CREATE TRIGGER fail_event BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT,'injected'); END")
			require.NoError(t, err)
			return deployer.Result{}, assert.AnError
		},
	)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "first"}
	require.Error(t, r.Reconcile(ctx, req))
	attempts, err := repo.List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, attempts.Deployments, 1)
	assert.Equal(t, deploymentmodel.Running, attempts.Deployments[0].Status)
	_, err = db.Exec(ctx, "DROP TRIGGER fail_event")
	require.NoError(t, err)
	docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
	services.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil).AnyTimes()
	req.Commit = "second"
	require.NoError(t, r.Reconcile(ctx, req))
	attempts, err = repo.List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, attempts.Deployments, 1)
	assert.Equal(t, deploymentmodel.Succeeded, attempts.Deployments[0].Status)
	assert.Equal(t, 2, attempts.Deployments[0].AttemptCount)
	stored, err := repo.Get(ctx, attempts.Deployments[0].ID)
	require.NoError(t, err)
	require.Len(t, stored.Attempts, 2)
	assert.Equal(t, deploymentmodel.Succeeded, stored.Attempts[0].Status)
	assert.Equal(t, deploymentmodel.Failed, stored.Attempts[1].Status)
}

func TestReconcileRecoversInterruptedCompletionConflictAndAppliesCorrectedDesiredState(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := outbox.New(db)
	hist, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployFailed, "failed-history", hist))
	require.NoError(t, bus.Subscribe(events.TypeNameDeployInterrupted, "interrupted-history", hist))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "success-history", hist))
	repo := deploymentstore.NewStore(db)
	ctrl := gomock.NewController(t)
	git := gitx.NewMockRepository(ctrl)
	docker := deployer.NewMockStackDeployer(ctrl)
	services := swarm.NewMockServiceManager(ctrl)
	dir := t.TempDir()
	git.EXPECT().WorkingDir().Return(dir).AnyTimes()
	require.NoError(t, writeComposeFile(dir))
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data")}}
	r := New(cfg, git, docker, &swarm.Swarm{
		Services: services, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
	}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)
	docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ string, _ string, _ string, _ compose.Compose) (deployer.Result, error) {
			_, triggerErr := db.Exec(ctx,
				"CREATE TRIGGER fail_event BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT,'injected'); END")
			require.NoError(t, triggerErr)
			return deployer.Result{}, errors.New("invalid image digest")
		},
	)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "broken"}
	require.Error(t, r.Reconcile(ctx, req))
	page, err := repo.List(ctx, deploymentmodel.ListFilter{Stack: "app"})
	require.NoError(t, err)
	require.Len(t, page.Deployments, 1)
	staleID := page.Deployments[0].ID
	assert.Equal(t, deploymentmodel.Running, page.Deployments[0].Status)
	_, err = db.Exec(ctx, "DROP TRIGGER fail_event")
	require.NoError(t, err)

	secondInstance := deployment.NewService(db, bus)
	require.NoError(t, secondInstance.InterruptRunning(ctx))
	interrupted, err := repo.Get(ctx, staleID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Interrupted, interrupted.Status)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"),
		[]byte("services:\n  api:\n    image: nginx@sha256:corrected\n"), 0o600))
	docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
	services.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil)
	req.Commit = "corrected"
	require.NoError(t, r.Reconcile(ctx, req))

	page, err = repo.List(ctx, deploymentmodel.ListFilter{Stack: "app"})
	require.NoError(t, err)
	require.Len(t, page.Deployments, 2)
	assert.Equal(t, deploymentmodel.Succeeded, page.Deployments[0].Status)
	assert.Equal(t, "corrected", page.Deployments[0].Commit)
	assert.NotEqual(t, staleID, page.Deployments[0].ID)
	assert.Equal(t, deploymentmodel.Superseded, page.Deployments[1].Status)
}

func TestReconcileRecoversSucceededCompletionConflictWithoutContradictoryOutcome(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := outbox.New(db)
	hist, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployFailed, "failed-history", hist))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "success-history", hist))
	service := deployment.NewService(db, bus)
	first, err := service.StartIfChanged(ctx, "app", "first", compose.File{
		Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "nginx:1"}}},
	}, true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, first.ID, model.Stack{LastCommit: "first"}))

	ctrl := gomock.NewController(t)
	git := gitx.NewMockRepository(ctrl)
	docker := deployer.NewMockStackDeployer(ctrl)
	services := swarm.NewMockServiceManager(ctrl)
	dir := t.TempDir()
	git.EXPECT().WorkingDir().Return(dir).AnyTimes()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"),
		[]byte("services:\n  api:\n    image: nginx:2\n"), 0o600))
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data")}}
	r := New(cfg, git, docker, &swarm.Swarm{
		Services: services, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
	}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)
	r.pending["app"] = pendingCompletion{
		attemptID: first.ID,
		state:     model.Stack{LastCommit: "contradictory"},
		status:    deploymentmodel.Failed,
		errorCode: "apply_failed",
		cause:     errors.New("obsolete failure"),
	}
	docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
	services.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil)
	require.NoError(t, r.Reconcile(ctx, ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "second",
	}))

	storedFirst, err := deploymentstore.NewStore(db).Get(ctx, first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, storedFirst.Status)
	baseline, err := deploymentstore.NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "nginx:2", baseline.Definition.Compose.Services[0].Image)
	var failedEvents int
	require.NoError(t, db.Get(ctx, &failedEvents,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameDeployFailed))
	assert.Zero(t, failedEvents, "obsolete recovery must not publish a contradictory failure")
	var successEvents int
	require.NoError(t, db.Get(ctx, &successEvents,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameDeploySuccess))
	assert.Equal(t, 2, successEvents, "each real successful attempt must publish exactly once")
}

func TestPreparationFailureStateAndPublicationAreAtomicAndSecretSafe(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := outbox.New(db)
	hist, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "history", hist))
	r := &Reconciler{db: db, stateStore: state, event: bus}
	_, err = db.Exec(ctx,
		"CREATE TRIGGER fail_preparation_event BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT,'injected'); END")
	require.NoError(t, err)
	require.Error(t, r.recordFailure(ctx, "app", "commit", nil, errors.New("raw-secret-value")))
	runtime, err := state.Read(ctx)
	require.NoError(t, err)
	_, exists := runtime.Stacks["app"]
	assert.False(t, exists)
	var count int
	require.NoError(t, db.Get(ctx, &count, "SELECT count(*) FROM outbox_events"))
	assert.Zero(t, count)
	_, err = db.Exec(ctx, "DROP TRIGGER fail_preparation_event")
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, r.recordFailure(ctx, "app", "commit", nil, errors.New("raw-secret-value")))
	}
	runtime, err = state.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "preparation_failed", runtime.Stacks["app"].LastError)
	var payloads string
	require.NoError(t, db.Get(ctx, &payloads, "SELECT group_concat(payload) FROM outbox_events"))
	assert.NotContains(t, payloads, "raw-secret-value")
	require.NoError(t, db.Get(ctx, &count, "SELECT count(*) FROM outbox_events"))
	assert.Equal(t, 2, count, "repeated failures are distinct domain facts")
}

func TestPendingFailureRecoveryPreservesTemporaryClassification(t *testing.T) {
	for _, step := range []string{"deploy stack", "load live state", "prune orphaned services"} {
		for _, temporary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/temporary=%t", step, temporary), func(t *testing.T) {
				ctx := context.Background()
				db, state := newSQLState(t)
				bus := outbox.New(db)
				hist, err := history.NewSQLStore(db, 10)
				require.NoError(t, err)
				typ := events.TypeNameDeployFailed
				if step == "load live state" {
					typ = events.TypeNameDeployInterrupted
				}
				require.NoError(t, bus.Subscribe(typ, "history", hist))
				service := deployment.NewService(db, bus)
				file := compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}}}
				attempt, err := service.StartIfChanged(ctx, "app", "commit", file, true)
				require.NoError(t, err)
				r := &Reconciler{db: db, stateStore: state, event: bus, deployments: service,
					deployMetrics: &metrics.NopDeploys{}, pending: map[string]pendingCompletion{}}
				_, err = db.Exec(ctx, "CREATE TRIGGER fail_event BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT,'injected'); END")
				require.NoError(t, err)
				cause := fmt.Errorf("wrapped: %w", &net.DNSError{Err: "token=private-value", IsTemporary: temporary})
				require.Error(t, r.recordPipelineFailure(ctx,
					ReconciliationRequest{Stack: config.StackSpec{Name: "app"}, Commit: "commit"}, model.Stack{},
					&pipelinePayload{Attempt: attempt, Desired: &file, ObservedAt: time.Now()},
					&pipe.StepError{StepName: step, Err: cause}))
				require.Contains(t, r.pending, "app")
				assert.NotContains(t, r.pending["app"].cause.Error(), "private-value")
				_, err = db.Exec(ctx, "DROP TRIGGER fail_event")
				require.NoError(t, err)
				require.NoError(t, r.recoverPending(ctx, "app"))
				assert.Empty(t, r.pending)
				var payload string
				require.NoError(t, db.Get(ctx, &payload, "SELECT payload FROM outbox_events WHERE event_type=?", typ))
				assert.NotContains(t, payload, "private-value")
				decoded, err := codec.Decode(typ, codec.Version, []byte(payload))
				require.NoError(t, err)
				var decodedCause error
				switch event := decoded.(type) {
				case *events.DeployFailed:
					decodedCause = event.Error
				case *events.DeployInterrupted:
					decodedCause = event.Error
				}
				var classified faults.Error
				require.ErrorAs(t, decodedCause, &classified)
				assert.Equal(t, temporary, classified.Temporary())
				require.NoError(t, r.recoverPending(ctx, "app"))
				var count int
				require.NoError(t, db.Get(ctx, &count, "SELECT count(*) FROM outbox_events"))
				assert.Equal(t, 1, count, "recovery must publish the outcome exactly once")
			})
		}
	}
}

func TestPreparationFailurePreservesTemporaryClassification(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(fmt.Sprint(temporary), func(t *testing.T) {
			ctx := context.Background()
			db, state := newSQLState(t)
			bus := outbox.New(db)
			hist, err := history.NewSQLStore(db, 10)
			require.NoError(t, err)
			require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "history", hist))
			r := &Reconciler{db: db, stateStore: state, event: bus}
			cause := fmt.Errorf("fetch: %w", &net.DNSError{Err: "token=private-value", IsTemporary: temporary})
			require.NoError(t, r.recordFailure(ctx, "app", "commit", nil, cause))
			var payload string
			require.NoError(t, db.Get(ctx, &payload, "SELECT payload FROM outbox_events"))
			assert.NotContains(t, payload, "private-value")
			decoded, err := codec.Decode(events.TypeNameDeployPreparationFailed, codec.Version, []byte(payload))
			require.NoError(t, err)
			event, ok := decoded.(*events.DeployPreparationFailed)
			require.True(t, ok)
			var classified faults.Error
			require.ErrorAs(t, event.Error, &classified)
			assert.Equal(t, temporary, classified.Temporary())
			assert.Equal(t, "preparation_failed", event.ErrorCode)
		})
	}
}
