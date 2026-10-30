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
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

type failedJobWithLogs struct {
	logs []string
}

func (e *failedJobWithLogs) Error() string  { return "job exited with status 1" }
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

func TestInitJobDiagnosticLines(t *testing.T) {
	for _, tt := range []struct {
		name string
		logs []string
		want []string
	}{
		{name: "empty"},
		{name: "filtered", logs: []string{"password=hidden", "  "}},
		{name: "multiline", logs: []string{" first\n\n second "}, want: []string{"first", "second"}},
		{
			name: "byte limit includes separators",
			logs: []string{"first", strings.Repeat("x", maxInitJobDiagnosticBytes)},
			want: []string{"first", strings.Repeat("x", maxInitJobDiagnosticBytes-len("first\n"))},
		},
		{
			name: "line limit",
			logs: []string{strings.Repeat("line\n", maxInitJobDiagnosticLines+1)},
			want: strings.Split(strings.TrimSuffix(strings.Repeat("line\n", maxInitJobDiagnosticLines), "\n"), "\n"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lines := initJobDiagnosticLines(tt.logs)
			assert.Equal(t, tt.want, lines)
			assert.LessOrEqual(t, len(strings.Join(lines, "\n")), maxInitJobDiagnosticBytes)
		})
	}
}

func TestFailedInitJobLogsReachDurableDeploymentDiagnostics(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	hist, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployFailed, "history", hist))
	service := deployment.NewService(db, bus)
	file := compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}}}
	attempt, err := deployments.NewService(db, bus).StartIfChanged(
		ctx, "app", "commit", file, true, service.ProjectInterruption,
	)
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

	saved, err := deploymentstore.NewStore(db).Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, "init_failed", saved.ErrorCode)
	assert.Contains(t, saved.ErrorDiagnostic, "migration failed at revision 17")
	assert.NotContains(t, saved.ErrorDiagnostic, "my-credential")

	publications := outboxPublications(t, db, events.TypeNameDeployFailed)
	require.Len(t, publications, 1)
	failed, ok := publications[0].Envelope.Event.(*events.DeployFailed)
	require.True(t, ok)
	assert.NotContains(t, failed.Error.Error(), "my-credential")
	assert.Equal(t, "init_failed", failed.ErrorCode)
	assert.Contains(t, failed.Error.Error(), "migration failed at revision 17")
}
