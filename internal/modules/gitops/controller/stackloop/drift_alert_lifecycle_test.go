package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileDriftRecoveryPersistsAndDeliversEvents(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                      string
		deferred, failureOnly, remaining, verificationFails, retryable, selfHealVerificationFails bool
	}{
		{name: "manual recovery"},
		{name: "manual recovery while rollout retry stays deferred", deferred: true},
		{name: "manual recovery while transient retry stays in backoff", deferred: true, retryable: true},
		{name: "remaining drift stays open", deferred: true, remaining: true},
		{name: "failed live observation stays open", deferred: true, verificationFails: true},
		{name: "failed self heal verification retains prior incident", selfHealVerificationFails: true},
		{name: "rollout failure alone cannot emit drift recovery", deferred: true, failureOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			manager := swarm.NewMockServiceManager(ctrl)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			db, store := newSQLState(t)
			bus := dispatcher.NewOutboxDispatcher(outbox.New(db))
			publisher := dispatcher.NewMockDispatcher(ctrl)
			fixedEvents, successEvents := 0, 0
			publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, e events.Event) error {
				if fixed, ok := e.(*events.ServiceDriftFixed); ok && fixed.ServiceName == "api" {
					fixedEvents++
				}
				if _, ok := e.(*events.DeploySuccess); ok {
					successEvents++
				}
				return bus.Publish(ctx, e)
			}).AnyTimes()
			cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(t.TempDir(), "data")}}
			alerts := alertstore.NewSQLStore(db)
			subscriber := alertmanagement.NewSQLSubscriber(alerts, bus, func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, cfg)
			require.NoError(t, dispatcher.SubscribeMany(bus, []events.TypeName{
				events.TypeNameServiceDrifted, events.TypeNameServiceMissed, events.TypeNameServiceDriftFixed,
				events.TypeNameDeployFailed, events.TypeNameDeploySuccess,
			}, "alerts", subscriber))
			drain := func() {
				for {
					processed, err := bus.ProcessNext(ctx)
					require.NoError(t, err)
					if !processed {
						return
					}
				}
			}
			dir := t.TempDir()
			writeManifest := func(image string) {
				manifest := fmt.Sprintf("services:\n  api:\n    image: %s\n    deploy:\n      replicas: 3\n  peer:\n    image: example/peer:1\n    deploy:\n      replicas: 1\n", image)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0o600))
			}
			writeManifest("example/api:1")
			original, err := compose.NewFileLoader().Load(ctx, filepath.Join(dir, "app.yaml"))
			require.NoError(t, err)
			require.NoError(t, store.Update(ctx, func(runtime *model.Runtime) {
				runtime.Stacks["app"] = model.Stack{SourceDigest: original.Digest, LastCommit: "one", Services: map[string]model.Service{
					"api": {SyncStatus: model.SyncStatusSynced}, "peer": {SyncStatus: model.SyncStatusSynced},
				}}
			}))
			repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
			// Recreate the reconciler and lifecycle controller on every tick. The clock
			// stays before the automatic retry deadline to exercise persisted backoff.
			clock := time.Now().UTC()
			reconcile := func() error {
				lifecycle := deployments.NewService(db, publisher, deployments.WithClock(func() time.Time { return clock }))
				return newTestReconciler(cfg, repository, lifecycle, stackDeployer, &swarm.Swarm{Services: manager}, publisher,
					&metrics.NopDeploys{}, store, fs.NewLocalFileSystem(), db).Reconcile(ctx, ReconciliationRequest{
					Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one",
				})
			}
			one, three := uint64(1), uint64(3)
			healthy := []swarm.StackService{{Name: "api", Replicas: &three}, {Name: "peer", Replicas: &one}}
			drifted := []swarm.StackService{{Name: "api", Replicas: &one}, {Name: "peer", Replicas: &one}}
			if !tc.failureOnly {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(drifted, nil)
				require.NoError(t, reconcile())
				assert.True(t, readSQLState(t, store).Stacks["app"].DriftedServices["api"])
				// Alert persistence is downstream of committed event delivery, not scaling.
				beforeDelivery, listErr := alerts.List(ctx, alertstore.ListFilter{})
				require.NoError(t, listErr)
				assert.Empty(t, beforeDelivery)
				drain()
				open, listErr := alerts.List(ctx, alertstore.ListFilter{Kind: alertmodel.AlertKindServiceDrift})
				require.NoError(t, listErr)
				require.Len(t, open, 1)
				assert.Equal(t, alertmodel.AlertStatusOpen, open[0].Status)
			}

			var failed deploymentmodel.Deployment
			var failedAttempt deploymentmodel.Attempt
			if tc.deferred {
				writeManifest("example/api:2")
				failure := waiter.ErrConvergenceFailed
				stage := "wait for service convergence"
				if tc.retryable {
					failure = context.DeadlineExceeded
					stage = "deploy stack"
				}
				stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).Return(
					deployer.Result{Verification: deploymentmodel.Verification{Known: true, UnresolvedServices: []string{"api"}}},
					&pipe.StepError{StepName: stage, Err: failure})
				require.Error(t, reconcile())
				drain()
				failed, _, err = deploymentstore.NewStore(db).Latest(ctx, "app")
				require.NoError(t, err)
				require.Equal(t, deploymentmodel.Failed, failed.Status)
				require.Equal(t, tc.retryable, failed.Retryable)
				failedAttempt, err = deploymentstore.NewStore(db).GetAttempt(ctx, failed.LatestAttemptID)
				require.NoError(t, err)
				require.Equal(t, deploymentmodel.Failed, failedAttempt.Status)
				assert.Equal(t, !tc.failureOnly, readSQLState(t, store).Stacks["app"].DriftedServices["api"], "failure must preserve drift provenance")
				cfg.Spec.Sync.Policy.SelfHeal = true
			}
			if tc.selfHealVerificationFails {
				cfg.Spec.Sync.Policy.SelfHeal = true
			}
			before := readSQLState(t, store).Stacks["app"]
			live := healthy
			if tc.remaining || tc.selfHealVerificationFails {
				live = drifted
			}
			for range 3 {
				if tc.verificationFails {
					manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, assert.AnError)
					require.Error(t, reconcile())
				} else if tc.selfHealVerificationFails {
					manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(drifted, nil)
					stackDeployer.EXPECT().DeployService(gomock.Any(), gomock.Cond(func(req deployer.ServiceRequest) bool { return req.ServiceName == "api" })).Return(deployer.Result{}, nil)
					manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, assert.AnError)
					require.Error(t, reconcile())
				} else {
					if tc.deferred && !tc.retryable {
						// Only the independent service is remediated; no DeployService(api)
						// or DeployStack is expected, even when api still has replica drift.
						manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live[:1], nil)
						stackDeployer.EXPECT().DeployService(gomock.Any(), gomock.Cond(func(req deployer.ServiceRequest) bool {
							return req.StackName == "app" && req.ServiceName == "peer"
						})).Return(deployer.Result{}, nil)
						manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, nil)
					} else {
						manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, nil)
					}
					require.NoError(t, reconcile())
				}
				drain()
				state := readSQLState(t, store).Stacks["app"]
				if tc.deferred {
					assert.Equal(t, before.Services["api"], state.Services["api"], "drift recovery must not clear failed rollout diagnostics")
					assert.Equal(t, before.LastError, state.LastError)
					assert.Equal(t, before.LastDeployAt, state.LastDeployAt)
					after, _, readErr := deploymentstore.NewStore(db).Latest(ctx, "app")
					require.NoError(t, readErr)
					assert.Equal(t, failed, after, "deployment, failure classification and backoff must remain unchanged")
					afterAttempt, attemptErr := deploymentstore.NewStore(db).GetAttempt(ctx, failed.LatestAttemptID)
					require.NoError(t, attemptErr)
					assert.Equal(t, failedAttempt, afterAttempt, "persisted failed Attempt must remain unchanged")
					if !tc.verificationFails && !tc.retryable {
						assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), state.Services["peer"].SyncStatus)
					}
				}
			}
			if tc.selfHealVerificationFails {
				assert.True(t, readSQLState(t, store).Stacks["app"].DriftedServices["api"])
			}
			wantFixed := 1
			if tc.remaining || tc.verificationFails || tc.failureOnly || tc.selfHealVerificationFails {
				wantFixed = 0
			}
			assert.Equal(t, wantFixed, fixedEvents)
			assert.Equal(t, 0, successEvents)
			stored, err := alerts.List(ctx, alertstore.ListFilter{Kind: alertmodel.AlertKindServiceDrift})
			require.NoError(t, err)
			if tc.failureOnly {
				assert.Empty(t, stored)
			} else {
				var apiAlerts []alertmodel.Alert
				for _, alert := range stored {
					if alert.Fingerprint == alertmodel.ServiceDriftFingerprint("app", "api") {
						apiAlerts = append(apiAlerts, alert)
					}
				}
				require.Len(t, apiAlerts, 1, "repeated recovery must neither duplicate nor reopen the incident")
				wantStatus := alertmodel.AlertStatusOpen
				if wantFixed > 0 {
					wantStatus = alertmodel.AlertStatusResolved
				}
				assert.Equal(t, wantStatus, apiAlerts[0].Status)
			}
			if tc.deferred && !tc.retryable {
				failureAlert, readErr := alerts.FindOpenByFingerprint(ctx, alertmodel.SyncFailedFingerprint("app"))
				require.NoError(t, readErr)
				assert.Equal(t, alertmodel.AlertStatusOpen, failureAlert.Status)
				assert.EqualValues(t, 1, failureAlert.Occurrences)
			}
		})
	}
}

func TestDriftLifecyclePublicationFailureRollsBackRuntime(t *testing.T) {
	ctx := t.Context()
	db, store := newSQLState(t)
	previous := model.Stack{LastError: "convergence_failed", Services: map[string]model.Service{
		"api": {SyncStatus: model.SyncStatusOutOfSync, SyncError: "rollout paused"},
	}, DriftedServices: map[string]bool{"api": true}}
	require.NoError(t, store.Update(ctx, func(state *model.Runtime) { state.Stacks["app"] = previous }))
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(assert.AnError)
	reconciler := &Reconciler{db: db, stateStore: store, event: publisher}
	payload := &pipelinePayload{
		Desired:        &compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api"}}}},
		RemainingDrift: map[string]drift.ServiceDrift{}, RetryDeferred: true, ProtectedServices: []string{"api"},
	}
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app"}}
	require.ErrorIs(t, reconciler.processResult(ctx, req, previous, payload.Desired, payload), assert.AnError)
	assert.Equal(t, previous, readSQLState(t, store).Stacks["app"], "episode must not be consumed when recovery publication fails")
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	require.NoError(t, reconciler.processResult(ctx, req, previous, payload.Desired, payload))
	recovered := readSQLState(t, store).Stacks["app"]
	assert.Empty(t, recovered.DriftedServices)
	assert.Equal(t, previous.Services, recovered.Services)
	require.NoError(t, reconciler.processResult(ctx, req, recovered, payload.Desired, payload))
}

func TestDriftLifecycleSurvivesPreparationFailure(t *testing.T) {
	db, store := newSQLState(t)
	previous := model.Stack{DriftedServices: map[string]bool{"api": true}}
	require.NoError(t, store.Update(t.Context(), func(state *model.Runtime) { state.Stacks["app"] = previous }))
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	reconciler := &Reconciler{db: db, stateStore: store, event: publisher}
	require.NoError(t, reconciler.recordFailure(t.Context(), "app", "commit", []compose.Service{{Name: "api"}}, assert.AnError))
	assert.Equal(t, previous.DriftedServices, readSQLState(t, store).Stacks["app"].DriftedServices)
}

func TestDriftLifecycleRequiresVerifiedAnalysis(t *testing.T) {
	for _, tc := range []struct {
		name      string
		previous  model.Stack
		remaining map[string]drift.ServiceDrift
	}{
		{name: "unverified prior episode", previous: model.Stack{DriftedServices: map[string]bool{"api": true}}},
		{name: "failure status is not drift provenance", previous: model.Stack{Services: map[string]model.Service{
			"api": {SyncStatus: model.SyncStatusOutOfSync},
		}}, remaining: map[string]drift.ServiceDrift{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
			reconciler := &Reconciler{event: publisher}
			require.NoError(t, reconciler.publishDriftEvents(t.Context(), ReconciliationRequest{Stack: config.StackSpec{Name: "app"}}, tc.previous, &pipelinePayload{
				Desired: &compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api"}}}}, RemainingDrift: tc.remaining,
			}))
		})
	}
}
