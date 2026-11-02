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

// Enabled pruning and ownership isolation share the recovery revision in
// TestRealSwarmGitOpsLifecycle. Keep the distinct global prune=false contract.
func TestRealSwarmGitOpsPruningDisabled(t *testing.T) {
	f := newLifecycleFixture(t, false)
	keep := lifecycleService("job", "sleep 600", "")
	f.manifest(t, keep+lifecycleService("removed", "sleep 600", ""))
	f.reconcile(t)
	f.runningIDs(t, "job", 1)
	before := f.settled(t, "job")
	f.runningIDs(t, "removed", 1)
	removed := f.settled(t, "removed")
	// Cover each existing protection independently: an unmanaged service
	// in this namespace, a service-level prune opt-out, and another stack.
	protected := []dockerswarm.Service{
		f.protectedService(t, f.inspect(t, "job"), "unmanaged", f.stack, false, true),
		f.protectedService(t, f.inspect(t, "job"), "optout", f.stack, true, false),
		f.protectedService(t, f.inspect(t, "job"), "foreign", f.stack+"foreign", true, true),
	}
	f.manifest(t, keep)
	f.request.Commit = "remove-service"
	f.reconcile(t)
	assert.Equal(t, removed.Service.ID, f.inspect(t, "removed").ID)
	assert.Equal(t, removed.Service.Spec, f.settled(t, "removed").Service.Spec)
	require.Empty(t, f.publications(t, events.TypeNameServicePruned))
	for range 3 {
		f.reconcile(t)
	}
	f.unchangedService(t, before)
	f.unchangedService(t, removed)
	state, err := f.state.Read(f.ctx)
	require.NoError(t, err)
	assert.NotContains(t, state.Stacks[f.stack].Services, "removed", "runtime reflects the desired service set")
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
	f.delivered(t, events.TypeNameServicePruned, 0)
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
