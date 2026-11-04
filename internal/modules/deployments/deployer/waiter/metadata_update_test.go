package waiter

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestMetadataOnlyRecoveryVerifiesExistingTaskTemplates(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		oldTemplate  bool
		missingSpec  bool
		want         ServiceState
	}{
		{name: "without rollout status", want: ServiceReady},
		{name: "completed metadata update", status: "completed", want: ServiceReady},
		{name: "metadata update still updating", status: "updating", want: ServicePending},
		{name: "paused rollout remains failed", status: "paused", want: ServiceFailed},
		{name: "failed rollout remains failed", status: "failed", want: ServiceFailed},
		{name: "metadata write cannot hide unfinished rollout", status: "completed", oldTemplate: true, want: ServicePending},
		{name: "missing status cannot hide unfinished rollout", oldTemplate: true, want: ServicePending},
		{name: "missing task template is not evidence", status: "completed", missingSpec: true, want: ServicePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:2")
			after := before
			after.UpdatedAt = serviceWaitTestTime
			after.ServiceSpec.Labels = map[string]string{"release": "new"}
			after.PreviousSpec = &before.ServiceSpec
			if tc.status != "" {
				after.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateState(tc.status), StartedAt: serviceWaitTestTime}
			}
			tasks := testRunningTasks(1, before.CreatedAt)
			template := after.ServiceSpec.TaskTemplate
			if tc.oldTemplate {
				template = dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:1"}}
			}
			tasks[0].Spec = swarm.TaskSpec{}
			if !tc.missingSpec {
				tasks[0].Spec = swarm.NewTaskSpec(template)
			}
			for _, baseline := range []swarm.StackService{before, after} {
				progress := evaluateTestService("api", baseline, after, tasks, true, after.ServiceSpec, nil)
				assert.Equal(t, tc.want, progress.State, progress.Message)
			}
		})
	}
}

func TestMetadataOnlyExternalRecoveryVerifiesSpecificationWithoutTaskGeneration(t *testing.T) {
	for _, state := range []string{"", "completed", "paused", "updating"} {
		t.Run(state, func(t *testing.T) {
			service := externalService(3, serviceWaitTestTime, "job:1")
			previous := service.ServiceSpec
			service.PreviousSpec = &previous
			if state != "" {
				service.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateState(state), StartedAt: serviceWaitTestTime.Add(-time.Minute)}
			}
			progress := specificationProgress("job", service, service, true, service.ServiceSpec, services.UpdateGeneration{})
			want := ServiceReady
			if state == "paused" {
				want = ServiceFailed
			} else if state == "updating" {
				want = ServicePending
			}
			assert.Equal(t, want, progress.State, progress.Message)
			assert.True(t, progress.SpecificationOnly)
		})
	}
}
