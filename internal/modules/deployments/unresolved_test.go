package deployments

import (
	"context"
	"fmt"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestNativeDeploymentCarriesUnresolvedRolloutAcrossDesiredDigests(t *testing.T) {
	db, _, bus := openService(t)
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	networks := swarm.NewMockNetworkManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	secrets := swarm.NewMockSecretManager(ctrl)
	networks.EXPECT().Get(gomock.Any(), "app_default").Return(swarm.Network{ID: "network", Name: "app_default", Scope: "swarm", Driver: "overlay", Labels: map[string]string{labelsdict.StackNamespace: "app"}}, nil).AnyTimes()
	configs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	sw := &swarm.Swarm{Services: manager, Networks: networks, Configs: configs, Secrets: secrets}
	native := deployer.NewDeployer(time.Millisecond, time.Second, nil, sw, nil, deployer.NewSecretResolver(fs.NewLocalFileSystem(), deployer.PlaintextDecoder{}))
	create := func() *Controller {
		return NewController(NewService(db, bus), native, manager, pruner.NewServicePruner(manager, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{})
	}
	alerts := alertstore.NewSQLStore(db)
	subscriber := alertmanagement.NewSQLSubscriber(alerts, &dispatcher.NopDispatcher{}, func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, &config.Config{})
	for _, typ := range []events.TypeName{events.TypeNameDeployFailed, events.TypeNameDeploySuccess} {
		require.NoError(t, bus.Subscribe(typ, "alerts", subscriber))
	}
	drain := func() {
		for {
			processed, err := bus.ProcessNext(t.Context())
			require.NoError(t, err)
			if !processed {
				return
			}
		}
	}
	desired := func(a, b string) compose.File {
		f := desiredServices("a", "b", "historical")
		f.Compose.Services[0].Image, f.Compose.Services[1].Image = a, b
		return f
	}
	specs := func(file compose.File) []dockerswarm.ServiceSpec {
		converted, err := converter.New(networks, configs, secrets).Convert(t.Context(), "app", file.Compose)
		require.NoError(t, err)
		return converted.Services
	}
	beforeSpec := specs(desired("example/a:1", "example/b:1"))
	failedDesired := desired("example/a:2", "example/b:1")
	failedSpec := specs(failedDesired)
	nextDesired := desired("example/a:2", "example/b:2")
	nextSpec := specs(nextDesired)
	recoveredDesired := desired("example/a:2", "example/b:3")
	recoveredSpec := specs(recoveredDesired)
	now := time.Now().UTC()
	observed := func(name string, spec dockerswarm.ServiceSpec, stamp time.Time, status string) swarm.StackService {
		replicas := uint64(1)
		return swarm.StackService{ID: name + "-id", Name: name, Mode: "replicated", Replicas: &replicas, DesiredTasks: 1, ServiceSpec: spec, CreatedAt: now.Add(-time.Hour), UpdatedAt: stamp, UpdateStatus: &swarm.ServiceUpdateStatus{State: status, StartedAt: stamp}}
	}
	aOld := observed("a", beforeSpec[0], now.Add(-time.Hour), "completed")
	bOld := observed("b", beforeSpec[1], now.Add(-time.Hour), "completed")
	historical := observed("historical", beforeSpec[2], now.Add(-time.Hour), "paused")
	aFailed := observed("a", failedSpec[0], now, "paused")
	bNew := observed("b", nextSpec[1], now.Add(time.Second), "completed")
	aRecovered := observed("a", failedSpec[0], now, "completed")
	bRecovered := observed("b", recoveredSpec[1], now.Add(2*time.Second), "completed")
	inspect := func(name string, spec dockerswarm.ServiceSpec, status dockerswarm.UpdateState) {
		manager.EXPECT().Inspect(gomock.Any(), "app_"+name).Return(dockerswarm.Service{ID: name + "-id", Spec: spec, UpdateStatus: &dockerswarm.UpdateStatus{State: status}}, nil)
	}
	list := func(live ...swarm.StackService) {
		manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, nil)
	}
	running := func(id string, created time.Time) swarm.ServiceTask {
		return swarm.ServiceTask{ID: id, DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, CreatedAt: created}
	}
	exit := 1
	failedTask := swarm.ServiceTask{ID: "failed-new-task", DesiredState: swarm.TaskStateShutdown, CurrentState: swarm.TaskStateFailed, CreatedAt: now, ExitCode: &exit}
	oldTasks := []swarm.ServiceTask{running("old-running", now.Add(-time.Hour)), failedTask}
	// First rollout mutates a and fails while an old generation remains Running.
	list(aOld, bOld, historical)
	inspect("a", beforeSpec[0], dockerswarm.UpdateStateCompleted)
	inspect("b", beforeSpec[1], dockerswarm.UpdateStateCompleted)
	inspect("historical", beforeSpec[2], dockerswarm.UpdateStatePaused)
	manager.EXPECT().Update(gomock.Any(), "a-id", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	list(aFailed, bOld, historical)
	manager.EXPECT().ListTasks(gomock.Any(), "a-id").Return(oldTasks, nil)
	req := ExecuteRequest{Stack: config.StackSpec{Name: "app"}, Commit: "one", Desired: failedDesired, SourceChanged: true}
	first, err := create().Execute(t.Context(), req)
	require.Error(t, err)
	require.NotNil(t, first.Attempt)
	assert.Equal(t, deploymentmodel.Failed, first.Status)
	assert.Equal(t, "convergence_failed", first.ErrorCode)
	drain()
	// Reconstruct the controller/lifecycle using SQLite only. Changing b must not erase a.
	list(aFailed, bOld, historical)
	inspect("a", failedSpec[0], dockerswarm.UpdateStatePaused)
	inspect("b", beforeSpec[1], dockerswarm.UpdateStateCompleted)
	inspect("historical", beforeSpec[2], dockerswarm.UpdateStatePaused)
	manager.EXPECT().Update(gomock.Any(), "b-id", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	list(aFailed, bNew, historical)
	manager.EXPECT().ListTasks(gomock.Any(), "a-id").Return(oldTasks, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "b-id").Return([]swarm.ServiceTask{running("new-b", bNew.UpdatedAt)}, nil)
	req.Commit, req.Desired = "two", nextDesired
	second, err := create().Execute(t.Context(), req)
	require.Error(t, err)
	require.NotNil(t, second.Attempt)
	assert.NotEqual(t, first.Attempt.DeploymentID, second.Attempt.DeploymentID)
	assert.Equal(t, deploymentmodel.Failed, second.Status)
	assert.Equal(t, "convergence_failed", second.ErrorCode)
	stored, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, stored.UnresolvedServices, "b has genuinely converged and historical was never affected")
	assert.Equal(t, 1, stored.AttemptCount)
	drain()
	for range 3 {
		result, executeErr := create().Execute(t.Context(), req)
		require.NoError(t, executeErr)
		assert.True(t, result.RetryDeferred)
		assert.Nil(t, result.Attempt)
		assert.Equal(t, []string{"a"}, result.UnresolvedServices)
	}
	drain()
	incident, err := alerts.FindOpenByFingerprint(t.Context(), alertmodel.SyncFailedFingerprint("app"))
	require.NoError(t, err)
	assert.EqualValues(t, 2, incident.Occurrences, "only two real failed Attempts")
	// After a genuinely recovers, another desired digest may complete. No update of a or historical is allowed.
	list(aRecovered, bNew, historical)
	inspect("a", failedSpec[0], dockerswarm.UpdateStateCompleted)
	inspect("b", nextSpec[1], dockerswarm.UpdateStateCompleted)
	inspect("historical", beforeSpec[2], dockerswarm.UpdateStatePaused)
	manager.EXPECT().Update(gomock.Any(), "b-id", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	list(aRecovered, bRecovered, historical)
	manager.EXPECT().ListTasks(gomock.Any(), "a-id").Return([]swarm.ServiceTask{running("recovered-a", now)}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "b-id").Return([]swarm.ServiceTask{running("recovered-b", bRecovered.UpdatedAt)}, nil)
	list(aRecovered, bRecovered, historical)
	req.Commit, req.Desired = "three", recoveredDesired
	recovered, err := create().Execute(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, recovered.Status)
	stored, _, err = deploymentstore.NewStore(db).Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Empty(t, stored.UnresolvedServices)
	original, err := deploymentstore.NewStore(db).Get(t.Context(), first.Attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Failed, original.Attempts[0].Status)
	assert.Equal(t, 1, original.AttemptCount)
	drain()
	allAlerts, err := alerts.List(t.Context(), alertstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, allAlerts, 1)
	assert.Equal(t, alertmodel.AlertStatusResolved, allAlerts[0].Status)
	assert.EqualValues(t, 2, allAlerts[0].Occurrences)
}

// An apply/preparation failure between rollouts cannot discard pending verification.
func TestUnresolvedServicesSurviveIntermediateFailuresAndRemoval(t *testing.T) {
	db, lifecycle, _ := openService(t)
	file := desiredServices("a", "b")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", file, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionConvergenceFailed, UnresolvedServices: []string{"a"}}))
	file.Compose.Services[1].Image = "example/b:2"
	second, err := NewService(db, &dispatcher.NopDispatcher{}).StartIfChanged(t.Context(), "app", "two", file, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), second.ID, Completion{Outcome: CompletionApplyFailed, UnresolvedServices: []string{}}))
	stored, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, stored.UnresolvedServices)
	// Explicitly removing a from desired releases its obligation.
	file.Compose.Services = file.Compose.Services[1:]
	third, err := lifecycle.StartIfChanged(context.Background(), "app", "three", file, true)
	require.NoError(t, err)
	stored, err = deploymentstore.NewStore(db).Get(t.Context(), third.DeploymentID)
	require.NoError(t, err)
	assert.Empty(t, stored.UnresolvedServices)
}

func TestUnresolvedMetadataSurvivesDatabaseReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	lifecycle := NewService(db, &dispatcher.NopDispatcher{})
	file := desiredServices("a", "b")
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "one", file, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionConvergenceFailed, UnresolvedServices: []string{"a"}}))
	require.NoError(t, db.Close())
	db, err = storage.OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	defer db.Close()
	lifecycle = NewService(db, &dispatcher.NopDispatcher{})
	skipped, err := lifecycle.StartIfChanged(t.Context(), "app", "tick", file, false)
	require.NoError(t, err)
	assert.Nil(t, skipped)
	file.Compose.Services[1].Image = "example/b:2"
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "two", file, true)
	require.NoError(t, err)
	stored, err := deploymentstore.NewStore(db).Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, stored.UnresolvedServices)
}

func TestPreApplyFailuresRetainOnlyExistingRolloutObligations(t *testing.T) {
	for _, outcome := range []CompletionOutcome{CompletionInitJobFailed, CompletionPolicyRejected} {
		for _, unresolved := range []bool{false, true} {
			t.Run(fmt.Sprint(outcome, unresolved), func(t *testing.T) {
				db, lifecycle, bus := openService(t)
				file := desiredServices("a", "b")
				first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", file, true)
				require.NoError(t, err)
				completion := Completion{Outcome: CompletionSucceeded}
				var want []string
				if unresolved {
					completion = Completion{Outcome: CompletionConvergenceFailed, UnresolvedServices: []string{"a"}}
					want = []string{"a"}
				}
				require.NoError(t, lifecycle.Complete(t.Context(), first.ID, completion))
				file.Compose.Services[1].Image = "example/b:2"
				second, err := lifecycle.StartIfChanged(t.Context(), "app", "two", file, true)
				require.NoError(t, err)
				require.NoError(t, lifecycle.Complete(t.Context(), second.ID, Completion{Outcome: outcome}))
				stored, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
				require.NoError(t, err)
				assert.ElementsMatch(t, want, stored.UnresolvedServices)
				assert.Equal(t, deploymentmodel.Failed, stored.Status)
				ctrl := gomock.NewController(t)
				manager := swarm.NewMockServiceManager(ctrl)
				controller := NewController(NewService(db, bus), deployer.NewMockStackDeployer(ctrl), manager,
					pruner.NewServicePruner(manager, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{})
				deferred, err := controller.Execute(t.Context(), ExecuteRequest{Stack: config.StackSpec{Name: "app"}, Desired: file})
				require.NoError(t, err)
				assert.True(t, deferred.RetryDeferred)
				assert.Nil(t, deferred.Attempt)
				assert.ElementsMatch(t, []string{"a", "b"}, deferred.UnresolvedServices, "self-heal cannot bypass a failed pre-apply check")
				// A later digest must not demand new tasks from unaffected healthy services.
				file.Compose.Services[1].Image = "example/b:3"
				third, err := NewService(db, bus).StartIfChanged(t.Context(), "app", "three", file, true)
				require.NoError(t, err)
				stored, err = deploymentstore.NewStore(db).Get(t.Context(), third.DeploymentID)
				require.NoError(t, err)
				assert.ElementsMatch(t, want, stored.UnresolvedServices)
			})
		}
	}
}
