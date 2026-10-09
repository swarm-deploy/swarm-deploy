package deployment

import (
	"context"
	"encoding/json"
	"testing"

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

func TestSnapshotMaskingAndSemanticChanges(t *testing.T) {
	beforeFile := desired("api:1")
	beforeFile.Compose.Services[0].Command = compose.NewCommand([]string{"--password", "command-secret"})
	beforeFile.Compose.Configs = compose.Configs{"cfg": {Data: []byte("raw-config-secret"), File: "app.cfg"}}
	before, err := Prepare(beforeFile)
	require.NoError(t, err)
	payload, err := json.Marshal(before)
	require.NoError(t, err)
	for _, secret := range []string{"plaintext-secret", "command-secret", "raw-config-secret"} {
		assert.NotContains(t, string(payload), secret)
	}
	assert.Equal(t, "plaintext-secret", beforeFile.Compose.Services[0].Environment.Map["API_TOKEN"], "preparation must not mutate apply input")
	afterFile := desired("api:2")
	afterFile.Compose.Services[0].Environment.Map["API_TOKEN"] = "new-secret"
	afterFile.Compose.Services[0].Environment.Map["MODE"] = "staging"
	after, err := Prepare(afterFile)
	require.NoError(t, err)
	changes := Compare(before, after)
	byPath := map[string]Change{}
	for _, change := range changes {
		byPath[change.Path] = change
	}
	image := byPath["services/api/image"]
	require.NotNil(t, image.Before)
	assert.Equal(t, "api:1", *image.Before)
	assert.Equal(t, "api:2", *image.After)
	mode := byPath["services/api/environment/Map/MODE"]
	require.NotNil(t, mode.After)
	assert.Equal(t, "staging", *mode.After)
	token := byPath["services/api/environment/Map/API_TOKEN"]
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
