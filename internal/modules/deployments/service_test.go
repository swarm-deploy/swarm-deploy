package deployments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func desiredServices(names ...string) compose.File {
	services := make(compose.Services, 0, len(names))
	for _, name := range names {
		services = append(services, compose.Service{Name: name, Image: "example/" + name + ":1"})
	}
	return compose.File{Path: "app.yaml", Digest: "source", Compose: compose.Compose{Services: services}}
}

func openService(t *testing.T, options ...ServiceOption) (storage.Database, *Service, *eventbus.OutboxDispatcher) {
	t.Helper()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	return db, NewService(db, bus, options...), bus
}

func subscribe(t *testing.T, db storage.Database, bus *eventbus.OutboxDispatcher, types ...events.TypeName) {
	t.Helper()
	projection, err := history.NewSQLStore(db, 20)
	require.NoError(t, err)
	for _, eventType := range types {
		require.NoError(t, bus.Subscribe(eventType, "deployments-service-test", projection))
	}
}

func TestStartIfChangedCreatesNoopsAndRetries(t *testing.T) {
	db, service, _ := openService(t)
	store := deploymentstore.NewStore(db)

	for _, testCase := range []struct {
		name          string
		sourceChanged bool
		wantAttempt   bool
	}{
		{name: "initial unchanged source", sourceChanged: false, wantAttempt: false},
		{name: "initial changed source", sourceChanged: true, wantAttempt: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			attempt, err := service.StartIfChanged(
				t.Context(), testCase.name, "commit", desiredServices("api"), testCase.sourceChanged,
			)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantAttempt, attempt != nil)
		})
	}

	first, err := service.StartIfChanged(t.Context(), "retry", "first", desiredServices("api"), true)
	require.NoError(t, err)
	require.NotNil(t, first)
	unchanged, err := service.StartIfChanged(t.Context(), "retry", "second", desiredServices("api"), true)
	require.NoError(t, err)
	assert.Nil(t, unchanged)

	require.NoError(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return service.Complete(ctx, first.ID, Completion{Outcome: CompletionProcessInterrupted})
	}))
	retry, err := service.StartIfChanged(t.Context(), "retry", "third", desiredServices("api"), true, StartOptions{ManualRetry: true})
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, first.DeploymentID, retry.DeploymentID)
	assert.Equal(t, 2, retry.Number)

	stored, err := store.Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Running, stored.Status)
	assert.Equal(t, 2, stored.AttemptCount)
}

func TestStartIfChangedSupersedesAndPublishesAtomically(t *testing.T) {
	db, service, bus := openService(t)
	subscribe(t, db, bus, events.TypeNameDeployInterrupted, events.TypeNameServiceRemovedFromDesired)
	store := deploymentstore.NewStore(db)

	first, err := service.StartIfChanged(
		t.Context(), "app", "first", desiredServices("api", "worker"), true,
	)
	require.NoError(t, err)
	second, err := service.StartIfChanged(
		t.Context(), "app", "second", desiredServices("api"), true,
	)
	require.NoError(t, err)
	require.NotNil(t, second)

	previous, err := store.Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Superseded, previous.Status)
	require.Len(t, previous.Attempts, 1)
	assert.Equal(t, deploymentmodel.Interrupted, previous.Attempts[0].Status)
	assert.Len(t, publications(t, db, events.TypeNameDeployInterrupted), 1)
	assert.Len(t, publications(t, db, events.TypeNameServiceRemovedFromDesired), 1)
}

func TestStartIfChangedCarriesUnresolvedServicesFromInterruptedRunningDeployment(t *testing.T) {
	for _, tc := range []struct {
		name           string
		removeWorker   bool
		wantUnresolved []string
	}{
		{name: "unchanged service retained", wantUnresolved: []string{"api", "worker"}},
		{name: "removed service excluded", removeWorker: true, wantUnresolved: []string{"api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, service, _ := openService(t)
			store := deploymentstore.NewStore(db)

			first, err := service.StartIfChanged(t.Context(), "app", "first", desiredServices("api", "worker"), true)
			require.NoError(t, err)
			require.NotNil(t, first)

			// A new digest supersedes the still-running attempt before it can verify its services.
			nextDesired := desiredServices("api", "worker")
			if tc.removeWorker {
				nextDesired = desiredServices("api")
			} else {
				nextDesired.Compose.Services[1].Image = "example/worker:2"
			}
			second, err := service.StartIfChanged(t.Context(), "app", "second", nextDesired, true)
			require.NoError(t, err)
			require.NotNil(t, second)

			previous, err := store.Get(t.Context(), first.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Superseded, previous.Status)
			assert.Equal(t, []string{"api", "worker"}, previous.UnresolvedServices)

			current, err := store.Get(t.Context(), second.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantUnresolved, current.UnresolvedServices,
				"supersession must inherit interrupted verification obligations")
		})
	}
}

func TestStartIfChangedUsesSuccessfulBaselineForSemanticChanges(t *testing.T) {
	db, service, _ := openService(t)
	store := deploymentstore.NewStore(db)

	baselineAttempt, err := service.StartIfChanged(
		t.Context(), "app", "baseline", desiredServices("api"), true,
	)
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), baselineAttempt.ID, Completion{
		Outcome: CompletionSucceeded,
	}))

	target, err := service.StartIfChanged(
		t.Context(), "app", "target", desiredServices("api", "worker"), true,
	)
	require.NoError(t, err)
	stored, err := store.Get(t.Context(), target.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.BasisSuccessfulBaseline, stored.ComparisonBasis)
	assert.Equal(t, baselineAttempt.DeploymentID, stored.BasisDeploymentID)
	assert.NotEmpty(t, stored.Changes)
	assert.NotZero(t, stored.Summary.Added)

	require.NoError(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return service.Complete(ctx, target.ID, Completion{Outcome: CompletionProcessInterrupted})
	}))
	retry, err := service.StartIfChanged(
		t.Context(), "app", "retry", desiredServices("api", "worker"), true, StartOptions{ManualRetry: true},
	)
	require.NoError(t, err)
	assert.Equal(t, target.DeploymentID, retry.DeploymentID)
	retried, err := store.Get(t.Context(), target.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, baselineAttempt.DeploymentID, retried.BasisDeploymentID)
	assert.Equal(t, stored.Changes, retried.Changes)
}

func TestStartIfChangedRollsBackSupersessionWhenPublicationFails(t *testing.T) {
	db, service, bus := openService(t)
	subscribe(t, db, bus, events.TypeNameServiceRemovedFromDesired)
	store := deploymentstore.NewStore(db)

	first, err := service.StartIfChanged(
		t.Context(), "app", "first", desiredServices("api", "worker"), true,
	)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "CREATE TRIGGER reject_service_removal BEFORE INSERT ON outbox_events WHEN NEW.event_type='ServiceRemovedFromDesired' BEGIN SELECT RAISE(ABORT, 'outbox unavailable'); END")
	require.NoError(t, err)

	_, err = service.StartIfChanged(t.Context(), "app", "second", desiredServices("api"), true)
	require.Error(t, err)
	latest, _, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, first.DeploymentID, latest.ID)
	assert.Equal(t, deploymentmodel.Running, latest.Status)
	assert.Empty(t, publications(t, db, events.TypeNameServiceRemovedFromDesired))
}

func TestStartIfChangedRollsBackInterruptionWhenDeploymentPersistenceFails(t *testing.T) {
	db, service, bus := openService(t)
	subscribe(t, db, bus, events.TypeNameDeployInterrupted)
	store := deploymentstore.NewStore(db)

	first, err := service.StartIfChanged(t.Context(), "app", "first", desiredServices("api"), true)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), `CREATE TRIGGER reject_deployment BEFORE INSERT ON deployments
		WHEN NEW."commit"='second' BEGIN SELECT RAISE(ABORT, 'deployment unavailable'); END`)
	require.NoError(t, err)

	_, err = service.StartIfChanged(t.Context(), "app", "second", desiredServices("worker"), true)
	require.Error(t, err)
	latest, _, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, first.DeploymentID, latest.ID)
	assert.Equal(t, deploymentmodel.Running, latest.Status)
	assert.Empty(t, publications(t, db, events.TypeNameDeployInterrupted))
}

func publications(t *testing.T, db storage.Database, eventType events.TypeName) []outbox.Publication {
	t.Helper()
	result, err := outbox.New(db).ListPublications(t.Context(), outbox.ListFilter{EventType: eventType})
	require.NoError(t, err)
	return result
}
