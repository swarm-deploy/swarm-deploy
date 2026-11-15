package deployer

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDeployStackExecutesOnlyTargets(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	jobs := NewMockInitJobExecutor(ctrl)
	waiter := NewMockServiceWaitExecutor(ctrl)
	jobs.EXPECT().Run(gomock.Any(), gomock.Cond(func(spec InitJobSpec) bool { return spec.ServiceName == "api" })).Return(InitJobResult{Status: InitJobSucceeded, ServiceName: "api", StartedAt: time.Now()}, nil)
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), gomock.Cond(func(spec dockerswarm.ServiceSpec) bool { return spec.Name == "demo_api" }), gomock.Any()).Return("api-id", nil)
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	waiter.EXPECT().Wait(gomock.Any(), gomock.Cond(func(req ServiceWaitRequest) bool { return len(req.ServiceNames) == 1 && req.ServiceNames[0] == "api" })).Return(ServiceWaitResult{}, nil)
	d := &Deployer{secretResolver: newTestSecretResolver(), initJobRunner: jobs, serviceWaiter: waiter}
	wireNativeTestDeployerWithServiceManager(t, d, manager, false)
	desired := minimalCompose("api", "worker")
	for i := range desired.Services {
		desired.Services[i].InitJobs = []compose.InitJob{{Name: "migrate", Image: "alpine:3"}}
	}
	// Invalid unrelated dependencies must not be read by this execution.
	desired.Services[1].Secrets = []compose.ObjectRef{{Source: "unrelated"}}
	desired.Secrets = compose.Secrets{"unrelated": {File: "unreadable.age"}}
	result, err := d.DeployStack(t.Context(), StackRequest{StackName: "demo", Desired: desired, TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.Len(t, result.InitJobs, 1)
	assert.Equal(t, "api", result.InitJobs[0].ServiceName)
}

func TestDeployStackEmptyTargetsHasNoExternalEffects(t *testing.T) {
	// No dependencies are wired: any external pipeline activity would panic.
	// Nil is not used here: it selects the whole desired stack.
	d := &Deployer{}
	result, err := d.DeployStack(t.Context(), StackRequest{Desired: minimalCompose("api"), TargetServices: []string{}})
	require.NoError(t, err)
	assert.True(t, result.Verification.Known)
	assert.Empty(t, result.InitJobs)
}
