package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileReplicaDriftPolicy(t *testing.T) {
	tests := []struct {
		name         string
		replicaLabel string
		serviceLabel string
		selfHeal     bool
		missing      bool
		repair       bool
		status       model.SyncStatus
		eventTypes   []events.TypeName
		alerts       int
	}{
		{name: "replicas ignored", replicaLabel: "false", selfHeal: true, status: model.SyncStatusSynced},
		{name: "both policies disabled", replicaLabel: "false", status: model.SyncStatusSynced},
		{name: "selfHeal false still reports replica drift", selfHeal: true, serviceLabel: "false", status: model.SyncStatusOutOfSync,
			eventTypes: []events.TypeName{events.TypeNameServiceDrifted}, alerts: 1},
		{name: "explicit replica detection reports drift", replicaLabel: "true", status: model.SyncStatusOutOfSync,
			eventTypes: []events.TypeName{events.TypeNameServiceDrifted}, alerts: 1},
		{name: "missing service still reported", replicaLabel: "false", missing: true, status: model.SyncStatusOutOfSync,
			eventTypes: []events.TypeName{events.TypeNameServiceMissed}, alerts: 1},
		{name: "restored service with different replicas passes verification", replicaLabel: "false", selfHeal: true,
			missing: true, repair: true, status: model.SyncStatusSynced,
			eventTypes: []events.TypeName{events.TypeNameServiceMissed, events.TypeNameServiceDriftFixed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			services := swarm.NewMockServiceManager(ctrl)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			db, stateStore := newSQLState(t)
			dir := t.TempDir()
			content := "services:\n  api:\n    image: nginx:latest\n    deploy:\n      replicas: 0\n"
			if tt.replicaLabel != "" || tt.serviceLabel != "" {
				content += "      labels:\n"
				if tt.replicaLabel != "" {
					content += "        org.swarm-deploy.service.sync.policy.selfHeal.replicas: \"" + tt.replicaLabel + "\"\n"
				}
				if tt.serviceLabel != "" {
					content += "        org.swarm-deploy.service.sync.policy.selfHeal: \"" + tt.serviceLabel + "\"\n"
				}
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(content), 0o600))
			desired, err := compose.NewFileLoader().Load(t.Context(), filepath.Join(dir, "app.yaml"))
			require.NoError(t, err)
			require.NoError(t, stateStore.Update(t.Context(), func(state *model.Runtime) {
				state.Stacks["app"] = model.Stack{SourceDigest: desired.Digest, LastCommit: "previous"}
			}))
			publisher := dispatcher.NewMockDispatcher(ctrl)
			var captured []events.Event
			publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					captured = append(captured, event)
					return nil
				}).AnyTimes()
			one := uint64(1)
			live := []swarm.StackService{{Name: "api", Replicas: &one}}
			initial := live
			if tt.missing {
				initial = nil
			}
			repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
			services.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(initial, nil)
			if tt.repair {
				services.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, nil)
				stackDeployer.EXPECT().DeployService(gomock.Any(), gomock.Cond(func(req deployer.ServiceRequest) bool {
					return req.StackName == "app" &&
						req.SourceComposePath == filepath.Join(dir, "app.yaml") &&
						req.ServiceName == "api"
				})).Return(deployer.Result{}, nil)
			}
			cfg := &config.Config{Spec: config.Spec{
				DataDir: filepath.Join(dir, ".data"),
				Sync:    config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: tt.selfHeal}},
			}}
			r := &Reconciler{
				cfg: cfg, db: db, event: publisher, stateStore: stateStore, deployMetrics: &metrics.NopDeploys{},
				pruner:        pruner.NewServicePruner(services, publisher, config.SyncPolicySpec{}),
				driftAnalyzer: drift.NewAnalyzer(), serviceManager: services,
			}
			r.executor = deployments.NewController(
				deployments.NewService(db, publisher), stackDeployer, services, r.pruner, r.deployMetrics)
			r.preparer = preparation.New(cfg, repository, fs.NewLocalFileSystem(), r.composeRotator)
			r.attachPipeline()
			require.NoError(t, r.Reconcile(t.Context(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "current",
			}))
			state := readSQLState(t, stateStore).Stacks["app"].Services["api"]
			assert.Equal(t, tt.status, state.SyncStatus)
			var types []events.TypeName
			alerts := alertstore.NewSQLStore(db)
			subscriber := alertmanagement.NewSQLSubscriber(alerts, &dispatcher.NopDispatcher{},
				func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, cfg)
			for i, event := range captured {
				types = append(types, event.Type().Name())
				require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{
					ID: fmt.Sprintf("drift-%d", i), OccurredAt: time.Now().UTC().Add(time.Duration(i) * time.Second), Event: event,
				}))
			}
			assert.Equal(t, tt.eventTypes, types)
			stored, err := alerts.List(t.Context(), alertstore.ListFilter{})
			require.NoError(t, err)
			assert.Len(t, stored, tt.alerts)
		})
	}
}
