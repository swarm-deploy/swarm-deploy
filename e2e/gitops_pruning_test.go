package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmGitOpsPruningAndOwnership(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prune bool
	}{
		{name: "enabled", prune: true},
		{name: "disabled", prune: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLifecycleFixture(t, tc.prune)
			keep := lifecycleService("job", "sleep 600", "")
			f.manifest(t, keep+lifecycleService("removed", "sleep 600", ""))
			f.reconcile(t)
			ids := f.runningIDs(t, "job", 1)
			before := f.settled(t, "job")
			f.runningIDs(t, "removed", 1)
			removed := f.settled(t, "removed")
			// Cover each existing protection independently: an unmanaged service
			// in this namespace, a service-level prune opt-out, and another stack.
			protected := []dockerswarm.Service{
				f.protectedService(t, before, "unmanaged", f.stack, false, true),
				f.protectedService(t, before, "optout", f.stack, true, false),
				f.protectedService(t, before, "foreign", f.stack+"foreign", true, true),
			}
			f.manifest(t, keep)
			f.request.Commit = "remove-service"
			f.reconcile(t)
			if tc.prune {
				f.poll(t, "only removed service deleted", func() bool {
					_, err := f.sw.Services.Inspect(f.ctx, removed.ID)
					return errors.Is(err, swarm.ErrServiceNotFound)
				})
				items := f.publications(t, events.TypeNameServicePruned)
				require.Len(t, items, 1)
				event, ok := items[0].Envelope.Event.(*events.ServicePruned)
				require.True(t, ok)
				assert.Equal(t, "removed", event.ServiceName)
				assert.Equal(t, f.request.Commit, event.Commit)
			} else {
				assert.Equal(t, removed.ID, f.inspect(t, "removed").ID)
				assert.Equal(t, removed.Spec, f.inspect(t, "removed").Spec)
				require.Empty(t, f.publications(t, events.TypeNameServicePruned))
			}
			for range 3 {
				f.reconcile(t)
			}
			f.unchangedService(t, before, ids)
			for _, service := range protected {
				current, err := f.sw.Services.Inspect(f.ctx, service.ID)
				require.NoError(t, err)
				assert.Equal(t, service.Spec, current.Spec)
			}
			assertSuccessfulHistory(t, f.latest(t))
			assert.Equal(t, 1, f.latest(t).AttemptCount)
			page, err := f.store.List(f.ctx, model.ListFilter{Stacks: []string{f.stack}})
			require.NoError(t, err)
			require.Len(t, page.Deployments, 2, "repeated reconciliation creates no deployments")
			count := 0
			if tc.prune {
				count = 1
			}
			f.delivered(t, events.TypeNameServicePruned, count)
		})
	}
}

func (f *lifecycleFixture) protectedService(t *testing.T, source dockerswarm.Service, name, namespace string, managed, prune bool) dockerswarm.Service {
	t.Helper()
	spec := source.Spec
	spec.Name = f.stack + "_" + name
	spec.Labels = map[string]string{"com.docker.stack.namespace": namespace}
	if managed {
		spec.Labels[labelsdict.ServiceManagedLabelKey] = "true"
	}
	if !prune {
		spec.Labels[labelsdict.ServiceSyncPolicyPruneLabelKey] = "false"
	}
	zero := uint64(0)
	spec.Mode.Replicated = &dockerswarm.ReplicatedService{Replicas: &zero}
	id, err := f.sw.Services.Create(f.ctx, spec, dockerswarm.ServiceCreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := f.sw.Services.Remove(ctx, id)
		if !errors.Is(err, swarm.ErrServiceNotFound) {
			assert.NoError(t, err)
		}
	})
	created, err := f.sw.Services.Inspect(f.ctx, id)
	require.NoError(t, err)
	return created
}
