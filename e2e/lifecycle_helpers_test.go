package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/dockertester"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	gitopsstore "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// Reuse the real Engine fixture, subnet allocation, bounded polling and cleanup
// used by the external-controller tests. All lifecycle/state/event stores are SQLite.
type lifecycleFixture struct {
	*externalSwarmFixture
	db         storage.Database
	executor   *deployments.Controller
	reconciler *stackloop.Reconciler
	store      *deploymentstore.Store
	state      *gitopsstore.SQLStore
	bus        *dispatcher.OutboxDispatcher
	outbox     *outbox.Repository
	history    *history.SQLStore
	request    stackloop.ReconciliationRequest
}

func newLifecycleFixture(t *testing.T, prune bool) *lifecycleFixture {
	t.Helper()
	base := newExternalSwarmFixture(t)
	cfg := &config.Config{Spec: config.Spec{
		DataDir: t.TempDir(), Sync: config.SyncSpec{Policy: config.SyncPolicySpec{Prune: prune, SelfHeal: true}},
	}}
	return newLifecycleComponents(t, base, cfg, gitx.NewLazyProxy(config.GitSpec{}, filepath.Dir(base.path)))
}

func newLifecycleComponents(t *testing.T, base *externalSwarmFixture, cfg *config.Config, repository gitx.Repository) *lifecycleFixture {
	t.Helper()
	f := &lifecycleFixture{externalSwarmFixture: base}
	db, err := storage.OpenSQLite(f.ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	f.db = db
	f.store, f.state, f.outbox = deploymentstore.NewStore(db), gitopsstore.NewSQLStore(db), outbox.New(db)
	f.bus = dispatcher.NewOutboxDispatcher(f.outbox)
	f.history, err = history.NewSQLStore(db, 100)
	require.NoError(t, err)
	for _, typ := range []events.TypeName{
		events.TypeNameDeploySuccess, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted,
		events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed, events.TypeNameServiceMissed,
		events.TypeNameServicePruned, events.TypeNameServiceRemovedFromDesired,
	} {
		require.NoError(t, f.bus.Subscribe(typ, "e2e-history", f.history))
	}
	f.executor = deployments.NewController(deployments.NewService(db, f.bus), f.deployer, f.sw.Services,
		pruner.NewServicePruner(f.sw.Services, f.bus, cfg.Spec.Sync.Policy), &metrics.NopDeploys{})
	fileSystem := fs.NewLocalFileSystem()
	rotator := stackloop.NewRotator()
	f.reconciler = stackloop.New(cfg, f.executor, f.sw, f.bus, &metrics.NopDeploys{}, f.state,
		preparation.New(cfg, repository, fileSystem, rotator), rotator, db)
	f.request = stackloop.ReconciliationRequest{
		Stack: config.StackSpec{Name: f.stack, ComposeFile: filepath.Base(f.path)}, Commit: "initial",
	}
	return f
}

func (f *lifecycleFixture) manifest(t *testing.T, services string) compose.File {
	t.Helper()
	content := fmt.Sprintf("services:\n%s\nnetworks:\n  default:\n    ipam:\n      config:\n        - subnet: %s\n", services, f.subnet)
	require.NoError(t, os.WriteFile(f.path, []byte(content), 0o600))
	file, err := compose.NewFileLoader().Load(f.ctx, f.path)
	require.NoError(t, err)
	return *file
}

func lifecycleService(name, command, policy string) string {
	return fmt.Sprintf(`  %s:
    image: busybox:1.36
    command: ["sh", "-c", %q]
    stop_grace_period: 1s
    deploy:
      replicas: 1
      restart_policy: {condition: none, delay: 100ms}
      update_config: {monitor: 1s, failure_action: pause}
%s
`, name, command, policy)
}

func (f *lifecycleFixture) reconcile(t *testing.T) {
	t.Helper()
	require.NoError(t, reconcileFromWorkingTree(f.ctx, f.reconciler, f.request))
}

func (f *lifecycleFixture) latest(t *testing.T) model.Deployment {
	t.Helper()
	item, _, err := f.store.Latest(f.ctx, f.stack)
	require.NoError(t, err)
	item, err = f.store.Get(f.ctx, item.ID)
	require.NoError(t, err)
	return item
}

func (f *lifecycleFixture) publications(t *testing.T, typ events.TypeName) []outbox.Publication {
	t.Helper()
	items, err := f.outbox.ListPublications(f.ctx, outbox.ListFilter{EventType: typ})
	require.NoError(t, err)
	for _, item := range items {
		require.Equal(t, f.stack, item.Envelope.Event.Details()[events.AttributeStackName])
	}
	return items
}

func (f *lifecycleFixture) delivered(t *testing.T, typ events.TypeName, count int) {
	t.Helper()
	// Drive the real worker synchronously, keeping publication and delivery
	// separate without background goroutines racing SQLite/fixture teardown.
	publications := f.publications(t, typ)
	if len(publications) > 0 {
		require.Len(t, publications, count)
	}
	for range 100 {
		worked, err := f.bus.ProcessNext(f.ctx)
		require.NoError(t, err)
		if !worked {
			deliveries, listErr := f.outbox.ListDeliveries(f.ctx, outbox.ListDeliveriesFilter{})
			require.NoError(t, listErr)
			// Successful acknowledgement removes completed publications and
			// cascades their deliveries; no delivery tombstone is retained.
			require.Empty(t, deliveries)
			// Failed/interrupted attempts belong to Deployment History; the
			// history subscriber intentionally excludes their raw domain events.
			if typ == events.TypeNameDeployFailed || typ == events.TypeNameDeployInterrupted {
				count = 0
			}
			items, readErr := f.history.ReadRecent(f.ctx, history.QueryOptions{Types: []events.TypeName{typ}, Limit: 100})
			require.NoError(t, readErr)
			require.Len(t, items, count)
			return
		}
	}
	t.Fatal("outbox did not drain within 100 deliveries")
}

func (f *lifecycleFixture) runningIDs(t *testing.T, name string, count int) []string {
	t.Helper()
	ids, err := f.observer.WaitRunningTasks(f.ctx, f.stack+"_"+name, count)
	require.NoError(t, err)
	return ids
}

func (f *lifecycleFixture) settled(t *testing.T, name string) dockertester.ServiceSnapshot {
	t.Helper()
	snapshot, err := f.observer.WaitStable(f.ctx, f.stack+"_"+name, time.Second)
	require.NoError(t, err)
	return snapshot
}

func (f *lifecycleFixture) unchangedService(t *testing.T, before dockertester.ServiceSnapshot) {
	t.Helper()
	require.NoError(t, f.observer.WaitUnchanged(f.ctx, before, 2*time.Second))
}

func (f *lifecycleFixture) syncStatus(t *testing.T, name string) string {
	t.Helper()
	state, err := f.state.Read(f.ctx)
	require.NoError(t, err)
	return string(state.Stacks[f.stack].Services[name].SyncStatus)
}

// reconcileFromWorkingTree loads the stack source like the sync runner does and reconciles it.
func reconcileFromWorkingTree(
	ctx context.Context, reconciler stackloop.StackReconciler, req stackloop.ReconciliationRequest,
) error {
	req.Source, req.SourceErr = reconciler.Load(ctx, req.Stack)
	return reconciler.Reconcile(ctx, req)
}

// cleanupFromWorkingTree loads the stack source like the sync runner does and cleans rotated resources.
func cleanupFromWorkingTree(ctx context.Context, reconciler stackloop.StackReconciler, stack config.StackSpec) error {
	source, err := reconciler.Load(ctx, stack)
	if err != nil {
		return err
	}
	return reconciler.Cleanup(ctx, stack, source)
}
