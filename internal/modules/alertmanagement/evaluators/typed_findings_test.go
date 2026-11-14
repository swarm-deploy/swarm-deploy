package evaluators

import (
	"errors"
	"fmt"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
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
		{swarm.TaskStateRejected, `failed to prepare extraction snapshot "extract-225092350-0LGf sha256:95dbfedee91178a3278e2e146a03bfd2223378ee9f60480b71df6f8d6cc1316e": NotFound: lease does not exist: not found`, faults.CodeImageExtractionFailed},
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
			actions, err := NewSyncEvaluator(&config.Config{}).Evaluate(t.Context(), events.Envelope{ID: "failure", Event: decoded})
			require.NoError(t, err)
			require.Len(t, actions, 1)
			alert := actions[0].Alert
			assert.Contains(t, alert.Message, "verify service app/api: "+tc.diagnostic)
			if tc.code == "" {
				assert.Empty(t, alert.Findings)
				return
			}
			wantSummary := tc.diagnostic
			if tc.code == faults.CodeImageExtractionFailed {
				wantSummary = "Container image extraction failed: containerd snapshot lease is missing. Retry deployment; check for concurrent image cleanup."
			}
			assert.Equal(t, []model.Finding{{Code: tc.code, Summary: wantSummary}}, alert.Findings)
		})
	}
}
