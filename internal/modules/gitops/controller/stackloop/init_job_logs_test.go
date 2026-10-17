package stackloop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

type failedJobWithLogs struct {
	logs []string
}

func (e *failedJobWithLogs) Error() string { return "job exited with status 1" }
func (e *failedJobWithLogs) Logs() []string { return e.logs }

func TestWithInitJobLogs(t *testing.T) {
	failure := &failedJobWithLogs{logs: []string{
		"migration failed: missing table",
		"token=do-not-persist",
		"database_url=postgres://do-not-persist",
		"password do-not-persist",
		strings.Repeat("x", 3000),
	}}
	wrapped := fmt.Errorf("deploy: %w", failure)
	diagnostic, found := withInitJobLogs(wrapped)
	require.True(t, found)
	require.ErrorIs(t, diagnostic, failure)
	msg := diagnostic.Error()
	assert.Contains(t, msg, "migration failed: missing table")
	assert.Contains(t, msg, "init job logs:")
	assert.NotContains(t, msg, "do-not-persist")
	assert.LessOrEqual(t, len(msg), maxInitJobDiagnosticBytes+len(wrapped.Error())+len("\ninit job logs:\n"))
	original, present := withInitJobLogs(errors.New("regular failure"))
	assert.False(t, present)
	assert.EqualError(t, original, "regular failure")
}

func TestFailedInitJobLogsReachDurableDeploymentDiagnostics(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := outbox.New(db)
	hist, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployFailed, "history", hist))
	service := deployment.NewService(db, bus)
	file := compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}}}
	attempt, err := service.StartIfChanged(ctx, "app", "commit", file, true)
	require.NoError(t, err)

	r := &Reconciler{db: db, stateStore: state, event: bus, deployments: service,
		deployMetrics: &metrics.NopDeploys{}, pending: map[string]pendingCompletion{}}
	jobErr := fmt.Errorf("init job: %w", &failedJobWithLogs{
		logs: []string{"migration failed at revision 17", "password=my-credential"},
	})
	failure := &pipe.StepError{StepName: "deploy stack", Err: jobErr}
	require.Error(t, r.recordPipelineFailure(ctx,
		ReconciliationRequest{Stack: config.StackSpec{Name: "app"}, Commit: "commit"}, model.Stack{},
		&pipelinePayload{Attempt: attempt, Desired: &file}, failure))

	saved, err := deployment.NewStore(db).Get(ctx, attempt.ID)
	require.NoError(t, err)
	assert.Equal(t, "init_failed", saved.ErrorCode)
	assert.Contains(t, saved.ErrorDiagnostic, "migration failed at revision 17")
	assert.NotContains(t, saved.ErrorDiagnostic, "my-credential")

	var payload string
	require.NoError(t, db.Get(ctx, &payload, "SELECT payload FROM outbox_events WHERE event_type=?", events.TypeNameDeployFailed))
	assert.NotContains(t, payload, "my-credential")
	event, err := codec.Decode(events.TypeNameDeployFailed, codec.Version, []byte(payload))
	require.NoError(t, err)
	failed, ok := event.(*events.DeployFailed)
	require.True(t, ok)
	assert.Equal(t, "init_failed", failed.ErrorCode)
	assert.Contains(t, failed.Error.Error(), "migration failed at revision 17")
}
