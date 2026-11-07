package alertmanagement

import (
	"errors"
	"fmt"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestDockerFaultsExtractedIntoAlertFindings(t *testing.T) {
	for _, tc := range []struct {
		state      swarm.TaskState
		diagnostic string
		code       faults.Code
	}{
		{swarm.TaskStatePending, "no suitable node (insufficient resources on 1 node)", "no_suitable_node"},
		{swarm.TaskStateRejected, "unauthorized: authentication required", "image_pull_denied"},
		{swarm.TaskStateRejected, `invalid mount config for type "bind": bind source path does not exist: /srv/data`, "mount_failed"},
		{swarm.TaskStateRejected, "No such image: api:2", "image_not_found"},
		{swarm.TaskStatePending, "waiting for assignment", ""},
		{swarm.TaskStateRejected, "image pull failed: context deadline exceeded", ""},
	} {
		t.Run(tc.diagnostic, func(t *testing.T) {
			task := swarm.ServiceTask{CurrentState: tc.state, Error: tc.diagnostic,
				Spec: swarm.NewTaskSpec(dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:2"}})}
			var cause error = errors.New(task.Error)
			if fault := task.Fault(); fault != nil {
				cause = fault
			}
			cause = faults.SafeError(fmt.Errorf("verify service app/api: %w", cause))
			var event events.Event = &events.DeployFailed{
				DeployEvent: events.DeployEvent{StackName: "app"}, ErrorCode: "convergence_failed", Error: cause,
			}
			typeName := events.TypeNameDeployFailed
			if tc.state == swarm.TaskStatePending {
				event = &events.DeployInterrupted{StackName: "app",
					Reason: "verification_unknown", Error: cause}
				typeName = events.TypeNameDeployInterrupted
			}
			// Use the durable event boundary that already preserves typed fault details.
			payload, err := codec.Encode(event)
			require.NoError(t, err)
			decoded, err := codec.Decode(typeName, codec.Version, payload)
			require.NoError(t, err)
			repo := modelstore.NewSQLStore(testutil.OpenSQLite(t))
			subscriber := newTestSQLSubscriber(repo)
			now := time.Date(2026, time.November, 8, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
			subscriber.now = func() time.Time { return now }
			require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{ID: "failure", OccurredAt: now, Event: decoded}))
			alerts, err := repo.List(t.Context(), modelstore.ListFilter{})
			require.NoError(t, err)
			require.Len(t, alerts, 1)
			assert.Contains(t, alerts[0].Message, "verify service app/api: "+tc.diagnostic)
			if tc.code == "" {
				assert.Empty(t, alerts[0].Findings)
				return
			}
			assert.Equal(t, []model.Finding{{Code: tc.code, Summary: tc.diagnostic, DetectedAt: now.UTC()}}, alerts[0].Findings)
		})
	}
}
