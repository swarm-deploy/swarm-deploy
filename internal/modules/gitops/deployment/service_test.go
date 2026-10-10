package deployment

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func desired(image string) compose.File {
	return compose.File{Path: "app.yaml", Digest: "source", Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: image, Environment: compose.Environment{Map: map[string]string{"API_TOKEN": "plaintext-secret", "MODE": "production"}},
	}}}}
}

func TestSuccessfulDeploymentIsIndependentOfHistoryFailure(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	bus := outbox.New(db)
	projection, err := history.NewSQLStore(db, 50)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", projection))
	service := NewService(db, bus)
	repo := NewStore(db)
	attempt, err := service.StartIfChanged(ctx, "app", "commit", desired("api:1"), true)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	current, err := repo.Get(ctx, attempt.ID)
	require.NoError(t, err)
	assert.Equal(t, Running, current.Status)
	require.NoError(t, service.Succeed(ctx, attempt.ID, model.Stack{SourceDigest: "source", LastCommit: "commit"}))
	entries, err := projection.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, entries, "publish must not invoke history")
	_, err = db.Get(ctx).ExecContext(ctx, "CREATE TRIGGER reject_history BEFORE INSERT ON event_history BEGIN SELECT RAISE(ABORT, 'projection failed'); END")
	require.NoError(t, err)
	_, err = bus.ProcessNext(ctx)
	require.NoError(t, err)
	current, err = repo.Get(ctx, attempt.ID)
	require.NoError(t, err)
	assert.Equal(t, Succeeded, current.Status)
	_, err = db.Get(ctx).ExecContext(ctx, "DROP TRIGGER reject_history")
	require.NoError(t, err)
	_, err = db.Get(ctx).ExecContext(ctx, "UPDATE outbox_deliveries SET available_at_ms=0")
	require.NoError(t, err)
	_, err = bus.ProcessNext(ctx)
	require.NoError(t, err)
	entries, err = projection.Read(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	var pending int
	require.NoError(t, db.Get(ctx).QueryRowContext(ctx, "SELECT count(*) FROM outbox_events").Scan(&pending))
	assert.Zero(t, pending)
	require.ErrorIs(t, service.Fail(ctx, attempt.ID, model.Stack{}, "apply_failed"), ErrNotRunning)
	same, err := service.StartIfChanged(ctx, "app", "next-commit", desired("api:1"), true)
	require.NoError(t, err)
	assert.Nil(t, same)
}

func TestFailedCommitAndCrashKeepSuccessfulBaseline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	bus := outbox.New(db)
	h, err := history.NewSQLStore(db, 50)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", h))
	service := NewService(db, bus)
	first, err := service.StartIfChanged(ctx, "app", "first", desired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, first.ID, model.Stack{LastCommit: "first", SourceDigest: "first"}))
	second, err := service.StartIfChanged(ctx, "app", "second", desired("api:2"), true)
	require.NoError(t, err)
	_, err = db.Get(ctx).ExecContext(ctx, "CREATE TRIGGER reject_publication BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT, 'publish failed'); END")
	require.NoError(t, err)
	require.Error(t, service.Succeed(ctx, second.ID, model.Stack{LastCommit: "second", SourceDigest: "second"}))
	baseline, err := NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "api:1", baseline.Definition.Compose.Services[0].Image)
	state, err := modelstore.NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "first", state.Stacks["app"].LastCommit)
	require.NoError(t, db.Close())
	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	service = NewService(db, outbox.New(db))
	require.NoError(t, service.InterruptRunning(ctx))
	current, err := NewStore(db).Get(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, Interrupted, current.Status)
	baseline, err = NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "api:1", baseline.Definition.Compose.Services[0].Image)
	retry, err := service.StartIfChanged(ctx, "app", "second", desired("api:2"), true)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.NotEqual(t, second.ID, retry.ID)
	require.NoError(t, service.Fail(ctx, retry.ID, model.Stack{}, "policy_rejected"))
	baseline, err = NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "api:1", baseline.Definition.Compose.Services[0].Image)
}

func TestRollbackToBaselineRetriesAfterUnsuccessfulAttempt(t *testing.T) {
	for _, status := range []Status{Failed, Interrupted} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(ctx, t.TempDir())
			require.NoError(t, err)
			defer db.Close()
			service := NewService(db, outbox.New(db))
			first, err := service.StartIfChanged(ctx, "app", "a", desired("api:a"), true)
			require.NoError(t, err)
			require.NoError(t, service.Succeed(ctx, first.ID, model.Stack{LastCommit: "a"}))
			second, err := service.StartIfChanged(ctx, "app", "b", desired("api:b"), true)
			require.NoError(t, err)
			require.NotNil(t, second)
			if status == Failed {
				require.NoError(t, service.Fail(ctx, second.ID, model.Stack{}, "prune_failed"))
			} else {
				require.NoError(t, service.InterruptStack(ctx, "app"))
			}
			rollback, err := service.StartIfChanged(ctx, "app", "rollback", desired("api:a"), true)
			require.NoError(t, err)
			require.NotNil(t, rollback)
			assert.NotEqual(t, second.ID, rollback.ID)
			assert.Equal(t, BasisLastAttempt, rollback.ComparisonBasis)
			assert.Equal(t, ComparisonUnknown, rollback.ComparisonStatus)
			assert.Equal(t, second.ID, rollback.BasisDeploymentID)
			require.NotEmpty(t, rollback.Changes, "recovery must describe the last attempt to desired transition")
			image := rollback.Changes[0]
			assert.Equal(t, "service", image.ResourceType)
			assert.Equal(t, "api", image.ResourceName)
			assert.Equal(t, "image", image.Field)
			assert.Equal(t, OperationChanged, image.Operation)
			require.NotNil(t, image.Before)
			require.NotNil(t, image.After)
			assert.Equal(t, "api:b", *image.Before)
			assert.Equal(t, "api:a", *image.After)
		})
	}
}

func TestListUsesLightweightCursorSummaries(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, outbox.New(db))
	clock := time.Date(2026, time.October, 10, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	for i, image := range []string{"api:1", "api:2", "api:3"} {
		attempt, startErr := service.StartIfChanged(ctx, "app", string(rune('a'+i)), desired(image), true)
		require.NoError(t, startErr)
		require.NoError(t, service.Succeed(ctx, attempt.ID, model.Stack{}))
	}
	store := NewStore(db)
	first, err := store.List(ctx, ListFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Deployments, 2)
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, "c", first.Deployments[0].Commit)
	assert.Equal(t, StageSucceeded, first.Deployments[0].ApplyStatus)
	assert.Equal(t, ActualStateObserved, first.Deployments[0].ActualStateStatus)
	assert.NotZero(t, first.Deployments[0].Summary.Changed)
	second, err := store.List(ctx, ListFilter{Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Deployments, 1)
	assert.Empty(t, second.NextCursor)
	assert.Equal(t, "a", second.Deployments[0].Commit)

	_, err = db.Get(ctx).ExecContext(ctx, "UPDATE deployments SET payload=json_set(payload,'$.changes','not-an-array')")
	require.NoError(t, err)
	_, err = store.List(ctx, ListFilter{})
	require.NoError(t, err, "list must not decode full change payloads")
	_, err = store.Get(ctx, first.Deployments[0].ID)
	require.Error(t, err, "detail still validates and decodes its change payload")
	_, err = store.List(ctx, ListFilter{Cursor: "malformed"})
	require.ErrorIs(t, err, ErrInvalidCursor)
}

func TestInterruptedTransitionIsAtomicDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	bus := outbox.New(db)
	handler, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployInterrupted, "interrupted-test", handler))
	service := NewService(db, bus)
	attempt, err := service.StartIfChanged(ctx, "app", "commit", desired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, service.InterruptRunning(ctx))
	require.NoError(t, service.InterruptRunning(ctx))
	current, err := NewStore(db).Get(ctx, attempt.ID)
	require.NoError(t, err)
	assert.Equal(t, Interrupted, current.Status)
	runtime, err := modelstore.NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "process_interrupted", runtime.Stacks["app"].LastError)
	var eventsCount int
	require.NoError(t, db.Get(ctx).QueryRowContext(ctx,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameDeployInterrupted).Scan(&eventsCount))
	assert.Equal(t, 1, eventsCount)
	_, err = NewStore(db).Baseline(ctx, "app")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSnapshotMaskingAndSemanticChanges(t *testing.T) {
	beforeFile := desired("api:1")
	beforeFile.Compose.Services[0].Command = compose.NewCommand([]string{"sh", "-c", "exec app --password 'review-secret-value'"})
	beforeFile.Compose.Services[0].Healthcheck = &compose.ServiceHealth{
		Test: compose.NewCommand([]string{"CMD-SHELL", "curl -u healthcheck-secret localhost"}),
	}
	beforeFile.Compose.Services[0].InitJobs = []compose.InitJob{{
		Name: "migrate", Image: "api:1", Entrypoint: []string{"sh", "-c"}, Command: []string{"migrate --token init-secret"},
	}}
	beforeFile.Compose.Configs = compose.Configs{"cfg": {Data: []byte("raw-config-secret"), File: "app.cfg"}}
	before, err := Prepare(beforeFile)
	require.NoError(t, err)
	payload, err := json.Marshal(before)
	require.NoError(t, err)
	for _, secret := range []string{
		"plaintext-secret", "review-secret-value", "healthcheck-secret", "init-secret", "raw-config-secret",
	} {
		assert.NotContains(t, string(payload), secret)
	}
	assert.Equal(t, "plaintext-secret", beforeFile.Compose.Services[0].Environment.Map["API_TOKEN"], "preparation must not mutate apply input")
	afterFile := desired("api:2")
	afterFile.Compose.Services[0].Environment.Map["API_TOKEN"] = "new-secret"
	afterFile.Compose.Services[0].Environment.Map["MODE"] = "staging"
	after, err := Prepare(afterFile)
	require.NoError(t, err)
	changes := Compare(before, after)
	byField := map[string]Change{}
	for _, change := range changes {
		byField[change.ResourceType+"/"+change.ResourceName+"/"+change.Field] = change
	}
	image := byField["service/api/image"]
	require.NotNil(t, image.Before)
	assert.Equal(t, "api:1", *image.Before)
	assert.Equal(t, "api:2", *image.After)
	mode := byField["service/api/environment.MODE"]
	require.NotNil(t, mode.After)
	assert.Equal(t, "staging", *mode.After)
	token := byField["service/api/environment.API_TOKEN"]
	require.NotNil(t, token.After)
	assert.True(t, token.Redacted)
	assert.Equal(t, "****", *token.After)
	// Source identity and parser ordering do not create effective changes.
	afterFile.Path = "/different/checkout/app.yaml"
	afterFile.Digest = "different-source-format"
	afterFile.Compose.Services[0].Environment.Keys = []string{"MODE", "API_TOKEN"}
	same, err := Prepare(afterFile)
	require.NoError(t, err)
	assert.Equal(t, after.Digest, same.Digest)
}

func TestPublicChangeFieldsHideGoModelRepresentation(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "environment map", path: "environment/Map/MODE", expected: "environment.MODE"},
		{name: "command args", path: "command/Args/0", expected: "command"},
		{name: "inline extra", path: "Extra/entrypoint/Args/0", expected: "entrypoint"},
		{name: "volume wrappers", path: "volumes/Volumes/0/ReadOnly", expected: "volumes.read_only"},
		{name: "network wrappers", path: "networks/List/0/IPV4Address", expected: "networks.ipv4_address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, publicField(strings.Split(tt.path, "/")))
		})
	}
}

func TestLegacyPathChangeDecodesToStructuredFields(t *testing.T) {
	var change Change
	require.NoError(t, json.Unmarshal([]byte(`{
		"path":"services/api/environment/Map/MODE","before":"production","after":"staging"
	}`), &change))
	assert.Equal(t, "service", change.ResourceType)
	assert.Equal(t, "api", change.ResourceName)
	assert.Equal(t, "environment.MODE", change.Field)
	assert.Equal(t, OperationChanged, change.Operation)
}

func TestOpaqueCommandChangesRemainDetectable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*compose.Service, string)
	}{
		{name: "command", mutate: func(service *compose.Service, secret string) {
			service.Command = compose.NewCommand([]string{"sh", "-c", "app --password " + secret})
		}},
		{name: "entrypoint", mutate: func(service *compose.Service, secret string) {
			service.Extra = map[string]any{"entrypoint": []string{"sh", "-c", "bootstrap --token " + secret}}
		}},
		{name: "healthcheck", mutate: func(service *compose.Service, secret string) {
			service.Healthcheck = &compose.ServiceHealth{Test: compose.NewCommand([]string{"CMD-SHELL", "check --token " + secret})}
		}},
		{name: "init job", mutate: func(service *compose.Service, secret string) {
			service.InitJobs = []compose.InitJob{{Name: "init", Image: "api:1", Entrypoint: []string{"sh", "-c"}, Command: []string{"init --token " + secret}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeFile, afterFile := desired("api:1"), desired("api:1")
			tt.mutate(&beforeFile.Compose.Services[0], "old-secret")
			tt.mutate(&afterFile.Compose.Services[0], "new-secret")
			before, err := Prepare(beforeFile)
			require.NoError(t, err)
			after, err := Prepare(afterFile)
			require.NoError(t, err)
			assert.NotEqual(t, before.Digest, after.Digest)
			changes := Compare(before, after)
			require.NotEmpty(t, changes)
			encoded, err := json.Marshal(struct {
				Before  Prepared
				After   Prepared
				Changes []Change
			}{before, after, changes})
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "old-secret")
			assert.NotContains(t, string(encoded), "new-secret")
		})
	}
}

func TestCommandRepresentationAffectsEffectiveDigest(t *testing.T) {
	parse := func(t *testing.T, command string) compose.File {
		t.Helper()
		parsed, err := compose.Parse([]byte("services:\n  api:\n    image: api:1\n    command: " + command + "\n"))
		require.NoError(t, err)
		return compose.File{Compose: *parsed}
	}
	scalar, err := Prepare(parse(t, "echo hello"))
	require.NoError(t, err)
	list, err := Prepare(parse(t, "[\"echo hello\"]"))
	require.NoError(t, err)
	assert.NotEqual(t, scalar.Digest, list.Digest)

	formatted, err := Prepare(parse(t, "'echo hello'"))
	require.NoError(t, err)
	assert.Equal(t, scalar.Digest, formatted.Digest)
}

func TestCommandSemanticsDriveDeploymentAttempts(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, outbox.New(db))
	parse := func(command string) compose.File {
		parsed, parseErr := compose.Parse([]byte("services:\n  api:\n    image: api:1\n    command: " + command + "\n"))
		require.NoError(t, parseErr)
		return compose.File{Compose: *parsed}
	}
	baseline, err := service.StartIfChanged(ctx, "app", "one", parse("echo hello"), true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, baseline.ID, model.Stack{}))
	formatOnly, err := service.StartIfChanged(ctx, "app", "two", parse("'echo hello'"), true)
	require.NoError(t, err)
	assert.Nil(t, formatOnly)
	changed, err := service.StartIfChanged(ctx, "app", "three", parse("[\"echo hello\"]"), true)
	require.NoError(t, err)
	require.NotNil(t, changed)
	assert.NotEqual(t, baseline.ID, changed.ID)
}
