package deployments

import (
	"context"
	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"

	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestUpdateGenerationSurvivesInterruptedApplyAndDesiredStateChanges(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	lifecycle := NewService(db, &dispatcher.NopDispatcher{})
	desired := desiredServices("api", "worker")
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true)
	require.NoError(t, err)
	generation := deploymentmodel.UpdateGeneration{ServiceID: "api-id", After: time.Now().UTC(), ForceUpdate: 7, Mode: deploymentmodel.VerifyNewGeneration, TasksAfter: time.Now().UTC().Add(-time.Second)}
	_, err = lifecycle.RecordUpdateGeneration(t.Context(), attempt.ID, "api", generation)
	require.NoError(t, err)
	// The process disappears without completion after a service write.
	require.NoError(t, db.Close())
	db, err = storage.OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle = NewService(db, &dispatcher.NopDispatcher{})
	require.NoError(t, lifecycle.InterruptRunning(t.Context()))
	store := deploymentstore.NewStore(db)
	saved, err := store.Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, generation, saved.Verification.Generations["api"])
	assert.Contains(t, saved.Verification.UnresolvedServices, "api")
	_, err = lifecycle.RecordUpdateGeneration(t.Context(), attempt.ID, "api", generation)
	require.ErrorIs(t, err, deploymentstore.ErrNotRunning)
	desired.Compose.Services[1].Image = "worker:2"
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "two", desired, true)
	require.NoError(t, err)
	saved, err = store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, generation, saved.Verification.Generations["api"])
	require.NoError(t, lifecycle.Complete(t.Context(), next.ID, Completion{Outcome: CompletionConvergenceFailed, Verification: deploymentmodel.Verification{Known: true, UnresolvedServices: []string{"worker"}}}))
	saved, err = store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Empty(t, saved.Verification.Generations, "verified services release their generation boundary")
}

func TestRejectedUpdateThenNoOpApplyVerifiesHealthyService(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle := NewService(db, &dispatcher.NopDispatcher{})
	store := deploymentstore.NewStore(db)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	nodes := swarm.NewMockNodeManager(gomock.NewController(t))
	one := uint64(1)
	boundary := time.Now().UTC().Add(-time.Minute)
	current := dockerswarm.Service{ID: "api-id", Meta: dockerswarm.Meta{CreatedAt: boundary.Add(-time.Hour), UpdatedAt: boundary}, Spec: dockerswarm.ServiceSpec{
		Annotations:  dockerswarm.Annotations{Name: "app_api", Labels: map[string]string{labelsdict.StackNamespace: "app"}},
		TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:1"}},
		Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &one}},
		EndpointSpec: &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8080}}},
	}}
	// A historical completed endpoint rollout must not become a new obligation.
	previous := current.Spec
	previous.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8079}}}
	started := boundary.Add(-time.Second)
	live := swarm.StackService{ID: current.ID, Name: "api", Mode: "replicated", Replicas: &one,
		CreatedAt: current.CreatedAt, UpdatedAt: current.UpdatedAt, ServiceSpec: current.Spec, PreviousSpec: &previous,
		UpdateStatus: &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateStateCompleted, StartedAt: started}}
	initialDesired := desiredServices("api")
	initial, err := lifecycle.StartIfChanged(t.Context(), "app", "A", initialDesired, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), initial.ID, Completion{Outcome: CompletionSucceeded}))
	changedDesired := desiredServices("api")
	changedDesired.Compose.Services[0].Image = "api:B"
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "B", changedDesired, true)
	require.NoError(t, err)
	wanted := current.Spec
	wanted.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8081}}}
	manager.EXPECT().Inspect(gomock.Any(), current.Spec.Name).Return(current, nil).Times(2)
	manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).Return(cerrdefs.ErrInvalidArgument)
	applier := services.New(manager, &registry.NopAuthManager{})
	applied, err := applier.Apply(t.Context(), "app", []dockerswarm.ServiceSpec{wanted}, services.ApplyOptions{
		BeforeUpdate: func(ctx context.Context, name string, generation services.UpdateGeneration) (func(context.Context) error, error) {
			return lifecycle.RecordUpdateGeneration(ctx, attempt.ID, name, generation)
		},
	})
	require.ErrorIs(t, err, cerrdefs.ErrInvalidArgument)
	require.Empty(t, applied.UpdateGenerations)
	saved, err := store.Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Empty(t, saved.Verification.Generations)
	assert.Empty(t, saved.Verification.UnresolvedServices)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionApplyFailed,
		Verification: deploymentmodel.Verification{Known: true}}))
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "A-again", initialDesired, true)
	require.NoError(t, err)
	saved, err = store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	applied, err = applier.Apply(t.Context(), "app", []dockerswarm.ServiceSpec{current.Spec}, services.ApplyOptions{UpdateGenerations: saved.Verification.Generations})
	require.NoError(t, err)
	assert.Empty(t, applied.ChangedServices)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{live}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return([]swarm.ServiceTask{{ID: "healthy-task", CreatedAt: started,
		DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, Spec: swarm.NewTaskSpec(current.Spec.TaskTemplate)}}, nil)
	verified, err := waiter.NewServiceWaiter(manager, nodes, time.Millisecond, time.Second).Wait(t.Context(), waiter.ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{live},
		ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"api": current.Spec}, UpdateGenerations: saved.Verification.Generations,
		UnresolvedServices: saved.Verification.UnresolvedServices,
	})
	require.NoError(t, err)
	assert.Empty(t, verified.UnresolvedServices)
}

func TestRejectedFollowupRestoresUnresolvedGeneration(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle := NewService(db, &dispatcher.NopDispatcher{})
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desiredServices("api"), true)
	require.NoError(t, err)
	first := deploymentmodel.UpdateGeneration{ServiceID: "api-id", After: time.Now().UTC(), Mode: deploymentmodel.VerifyNewGeneration}
	_, err = lifecycle.RecordUpdateGeneration(t.Context(), attempt.ID, "api", first)
	require.NoError(t, err)
	second := first
	second.After = second.After.Add(time.Second)
	restore, err := lifecycle.RecordUpdateGeneration(t.Context(), attempt.ID, "api", second)
	require.NoError(t, err)
	require.NoError(t, restore(t.Context()))
	require.NoError(t, lifecycle.InterruptRunning(t.Context()))
	saved, err := deploymentstore.NewStore(db).Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, first, saved.Verification.Generations["api"])
	assert.Equal(t, []string{"api"}, saved.Verification.UnresolvedServices)
}
