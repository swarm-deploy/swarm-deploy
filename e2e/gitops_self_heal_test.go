package e2e

import (
	"strings"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmGitOpsReplicaDriftAndSelfHeal(t *testing.T) {
	for _, tc := range []struct {
		name, label string
		heal, drift bool
	}{
		{name: "self-heal enabled", heal: true, drift: true},
		{name: "replica drift suppressed", label: "org.swarm-deploy.service.sync.policy.selfHeal.replicas", heal: false},
		{name: "self-heal disabled still detects drift", label: "org.swarm-deploy.service.sync.policy.selfHeal", drift: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLifecycleFixture(t, true)
			policy := ""
			if tc.label != "" {
				policy = "      labels:\n        " + tc.label + ": \"false\""
			}
			manifest := strings.Replace(lifecycleService("job", "sleep 600", policy), "replicas: 1", "replicas: 2", 1)
			f.manifest(t, manifest)
			f.reconcile(t)
			f.runningIDs(t, "job", 2)
			f.settled(t, "job")
			baseline := f.latest(t)
			assertSuccessfulHistory(t, baseline)
			three := uint64(3)
			f.externalUpdate(t, func(spec *dockerswarm.ServiceSpec) { spec.Mode.Replicated.Replicas = &three })
			externalIDs := f.runningIDs(t, "job", 3)
			external := f.settled(t, "job")
			f.reconcile(t)
			if tc.heal {
				f.runningIDs(t, "job", 2)
				healed := f.settled(t, "job")
				assert.Equal(t, external.ID, healed.ID)
				assert.Equal(t, uint64(2), *healed.Spec.Mode.Replicated.Replicas)
				assert.Equal(t, gitopsmodel.SyncStatusSynced, f.syncStatus(t, "job"))
				detected := f.publications(t, events.TypeNameServiceDrifted)
				require.Len(t, detected, 1)
				event, ok := detected[0].Envelope.Event.(*events.ServiceDrifted)
				require.True(t, ok)
				assert.True(t, event.Resolved, "detected and repaired in the same reconciliation")
				assert.Contains(t, event.Reason, "desired=2 actual=3")
				fixed := f.publications(t, events.TypeNameServiceDriftFixed)
				require.Len(t, fixed, 1)
				assert.Less(t, detected[0].Envelope.PublicationSequence, fixed[0].Envelope.PublicationSequence)
				assert.Equal(t, "job", fixed[0].Envelope.Event.Details()[events.AttributeServiceName])
			} else {
				for range 3 {
					f.reconcile(t)
				}
				f.unchangedService(t, external, externalIDs)
				assert.Equal(t, uint64(3), *f.inspect(t, "job").Spec.Mode.Replicated.Replicas)
				require.Empty(t, f.publications(t, events.TypeNameServiceDriftFixed))
				if tc.drift {
					assert.Equal(t, gitopsmodel.SyncStatusOutOfSync, f.syncStatus(t, "job"))
					items := f.publications(t, events.TypeNameServiceDrifted)
					require.Len(t, items, 1, "unchanged drift must not republish the detection")
					event, ok := items[0].Envelope.Event.(*events.ServiceDrifted)
					require.True(t, ok)
					assert.False(t, event.Resolved)
				} else {
					assert.Equal(t, gitopsmodel.SyncStatusSynced, f.syncStatus(t, "job"))
					require.Empty(t, f.publications(t, events.TypeNameServiceDrifted))
				}
			}
			assert.Equal(t, baseline.ID, f.latest(t).ID, "self-heal does not create a stack Deployment")
			assert.Equal(t, 1, f.latest(t).AttemptCount)
			if !tc.drift {
				f.verifyReplicaPolicyDoesNotHideMissingService(t, manifest)
			}
			if tc.heal {
				for range 3 {
					f.reconcile(t)
				}
				f.delivered(t, events.TypeNameServiceDrifted, 1)
				f.delivered(t, events.TypeNameServiceDriftFixed, 1)
			} else if tc.drift {
				f.delivered(t, events.TypeNameServiceDrifted, 1)
				f.delivered(t, events.TypeNameServiceDriftFixed, 0)
			}
		})
	}
}

func (f *lifecycleFixture) verifyReplicaPolicyDoesNotHideMissingService(t *testing.T, manifest string) {
	t.Helper()
	// Replica suppression must not suppress a source update to a different
	// field. Generic manual env/command drift detection is not implemented by
	// the current analyzer; this asserts the existing manifest-update path.
	f.manifest(t, strings.Replace(manifest, "sleep 600", "echo source-update; sleep 600", 1))
	f.request.Commit = "command-updated"
	f.reconcile(t)
	f.runningIDs(t, "job", 2)
	service := f.settled(t, "job")
	assert.Contains(t, service.Spec.TaskTemplate.ContainerSpec.Args, "echo source-update; sleep 600")
	require.NoError(t, f.sw.Services.Remove(f.ctx, service.ID))
	f.poll(t, "service missing from real stack observation", func() bool {
		services, err := f.sw.Services.ListServices(f.ctx, swarm.ListServicesFilter{Stack: f.stack})
		return err == nil && len(services) == 0
	})
	f.reconcile(t)
	f.runningIDs(t, "job", 2)
	restored := f.settled(t, "job")
	assert.NotEqual(t, service.ID, restored.ID)
	assert.Equal(t, gitopsmodel.SyncStatusSynced, f.syncStatus(t, "job"))
	require.Empty(t, f.publications(t, events.TypeNameServiceDrifted), "missing is a distinct domain event")
	missed := f.publications(t, events.TypeNameServiceMissed)
	require.Len(t, missed, 1)
	event, ok := missed[0].Envelope.Event.(*events.ServiceMissed)
	require.True(t, ok)
	assert.True(t, event.Resolved)
	assert.Equal(t, "job", event.ServiceName)
	f.delivered(t, events.TypeNameServiceMissed, 1)
	f.delivered(t, events.TypeNameServiceDriftFixed, 1)
}
