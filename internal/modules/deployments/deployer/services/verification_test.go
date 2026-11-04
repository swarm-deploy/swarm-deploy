package services

import (
	"context"
	"errors"
	"fmt"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func TestClassifyVerification(t *testing.T) {
	for _, tc := range []struct {
		name             string
		before, after    *dockerswarm.EndpointSpec
		placement, image bool
		want             VerificationMode
	}{
		{name: "unchanged", want: VerifyExistingTasks},
		{name: "nil and empty endpoint", after: &dockerswarm.EndpointSpec{}, want: VerifyExistingTasks},
		{name: "implicit VIP and empty ports", after: &dockerswarm.EndpointSpec{Mode: dockerswarm.ResolutionModeVIP, Ports: []dockerswarm.PortConfig{}}, want: VerifyExistingTasks},
		{name: "implicit port defaults", before: &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8080}}}, after: endpointSpec(8080), want: VerifyExistingTasks},
		{name: "published port", before: endpointSpec(8080), after: endpointSpec(8081), want: VerifyNewGeneration},
		{name: "add port", after: endpointSpec(8080), want: VerifyNewGeneration},
		{name: "remove port", before: endpointSpec(8080), want: VerifyNewGeneration},
		{name: "DNS round robin", after: &dockerswarm.EndpointSpec{Mode: dockerswarm.ResolutionModeDNSRR}, want: VerifyNewGeneration},
		{name: "placement", placement: true, want: VerifyPlacementCompatibleTasks},
		{name: "endpoint and placement", before: endpointSpec(8080), after: endpointSpec(8081), placement: true, want: VerifyPlacementCompatibleTasks},
		{name: "endpoint placement and image", before: endpointSpec(8080), after: endpointSpec(8081), placement: true, image: true, want: VerifyNewGeneration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:1"}}, EndpointSpec: tc.before}
			after := before
			after.EndpointSpec = tc.after
			after.Labels = map[string]string{"release": "new"}
			if tc.placement {
				after.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.role == manager"}}
			}
			if tc.image {
				after.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: "api:2"}
			}
			assert.Equal(t, tc.want, ClassifyVerification(before, after))
		})
	}
}

func endpointSpec(port uint32) *dockerswarm.EndpointSpec {
	return &dockerswarm.EndpointSpec{Mode: dockerswarm.ResolutionModeVIP, Ports: []dockerswarm.PortConfig{{
		TargetPort: 80, PublishedPort: port, Protocol: dockerswarm.PortConfigProtocolTCP, PublishMode: dockerswarm.PortConfigPublishModeIngress,
	}}}
}

func TestUpdateBoundaryIsRecordedBeforeDockerWrite(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			before := service("api-id", 7, serviceSpec("api", "api:1"))
			before.UpdatedAt = time.Now().UTC()
			before.Spec.EndpointSpec = endpointSpec(8080)
			desired := cloneSpec(before.Spec)
			desired.EndpointSpec = endpointSpec(8081)
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(before, nil)
			recorded := false
			if !fail {
				manager.EXPECT().Update(gomock.Any(), before.ID, before.Version, gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, string, dockerswarm.Version, dockerswarm.ServiceSpec, dockerswarm.ServiceUpdateOptions) error {
					assert.True(t, recorded, "generation must be durable before Docker can accept the spec")
					return nil
				})
			}
			cause := errors.New("persistence unavailable")
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{
				BeforeUpdate: func(_ context.Context, name string, generation UpdateGeneration) (func(context.Context) error, error) {
					assert.Equal(t, "api", name)
					assert.Equal(t, UpdateGeneration{ServiceID: before.ID, After: before.UpdatedAt, Mode: VerifyNewGeneration}, generation)
					recorded = true
					if fail {
						return nil, cause
					}
					return nil, nil
				},
			})
			if fail {
				require.ErrorIs(t, err, cause)
			} else {
				require.NoError(t, err)
				assert.Equal(t, before.UpdatedAt, result.UpdateGenerations["api"].After)
			}
		})
	}
}

func TestUpdatePreservesUnresolvedGeneration(t *testing.T) {
	boundary := time.Now().UTC().Add(-time.Minute)
	started := boundary.Add(time.Second)
	for _, tc := range []struct {
		name       string
		status     *dockerswarm.UpdateStatus
		nextPort   uint32
		wantMode   VerificationMode
		wantCutoff time.Time
	}{
		{name: "labels before rollout starts", wantMode: VerifyNewGeneration, wantCutoff: boundary.Add(2 * time.Second)},
		{name: "labels during rollout", status: &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStateUpdating, StartedAt: &started}, wantMode: VerifyNewGeneration, wantCutoff: started},
		{name: "labels after completion", status: &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStateCompleted, StartedAt: &started}, wantMode: VerifyNewGeneration, wantCutoff: started},
		{name: "another port change", nextPort: 8082, wantMode: VerifyNewGeneration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := service("api-id", 8, serviceSpec("api", "api:1"))
			current.UpdatedAt = boundary.Add(2 * time.Second)
			current.Spec.EndpointSpec = endpointSpec(8081)
			current.UpdateStatus = tc.status
			desired := cloneSpec(current.Spec)
			desired.Labels["release"] = "two"
			if tc.nextPort != 0 {
				desired.EndpointSpec = endpointSpec(tc.nextPort)
			}
			previous := UpdateGeneration{ServiceID: current.ID, After: boundary, Mode: VerifyNewGeneration}
			generation := updateGeneration(desired, current, previous)
			assert.Equal(t, tc.wantMode, generation.Mode)
			assert.Equal(t, tc.wantCutoff, generation.TasksAfter)
			assert.Equal(t, current.UpdatedAt, generation.After, "the latest write still needs acceptance evidence")
			if tc.nextPort == 0 {
				current.UpdatedAt = current.UpdatedAt.Add(time.Second)
				current.Spec = desired
				current.UpdateStatus = nil
				third := updateGeneration(desired, current, generation)
				assert.Equal(t, generation.TasksAfter, third.TasksAfter, "credential refresh must retain the original task boundary too")
			}
		})
	}
}

func TestRejectedUpdateRestoresPreviousObligation(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			current := service("api-id", 7, serviceSpec("api", "api:1"))
			current.UpdatedAt = time.Now().UTC()
			desired := cloneSpec(current.Spec)
			desired.EndpointSpec = endpointSpec(8081)
			cause := error(cerrdefs.ErrInvalidArgument)
			if uncertain {
				cause = context.DeadlineExceeded
			}
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).Return(cause)
			previous := UpdateGeneration{ServiceID: current.ID, After: current.UpdatedAt.Add(-time.Minute), Mode: VerifyNewGeneration}
			restored := false
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{
				UpdateGenerations: map[string]UpdateGeneration{"api": previous},
				BeforeUpdate: func(context.Context, string, UpdateGeneration) (func(context.Context) error, error) {
					return func(context.Context) error { restored = true; return nil }, nil
				},
			})
			require.ErrorIs(t, err, cause)
			assert.Equal(t, !uncertain, restored)
			if uncertain {
				assert.Equal(t, current.UpdatedAt, result.UpdateGenerations["api"].After)
			} else {
				assert.Equal(t, previous, result.UpdateGenerations["api"])
			}
		})
	}
}
