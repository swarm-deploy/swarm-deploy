package deployer

import (
	"context"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestApplyFailureRetainsOnlyPossibleWrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		known bool
	}{
		{name: "Docker rejected", err: cerrdefs.ErrInvalidArgument, known: true},
		{name: "outcome unknown", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			current := dockerswarm.Service{ID: "api-id", Spec: nativeMinimalServiceSpec("api")}
			desired := nativeMinimalServiceSpec("api")
			desired.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8081}}}
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).Return(tc.err)
			d := &Deployer{serviceApplier: services.New(manager, &registry.NopAuthManager{})}
			payload := &deployStackPayload{
				applyPayload: applyPayload{deadline: time.Now().Add(time.Minute)}, stackName: "demo",
				converted:          &converter.StackSpec{Services: []dockerswarm.ServiceSpec{desired}},
				unresolvedServices: []string{"worker"},
			}
			require.ErrorIs(t, d.applyStack(t.Context(), payload), tc.err)
			assert.Equal(t, tc.known, payload.result.Verification.Known)
			if tc.known {
				assert.Equal(t, []string{"worker"}, payload.result.Verification.UnresolvedServices)
				assert.Empty(t, payload.updateGenerations)
			} else {
				assert.Contains(t, payload.updateGenerations, "api", "a lost response must retain the write intent")
			}
		})
	}
}
