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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestDockerFaultsExtractedIntoAlertFindings(t *testing.T) {
	evaluator := NewSyncEvaluator(modelstore.NewSQLStore(testutil.OpenSQLite(t)), &config.Config{})
	for _, tc := range []struct {
		state       swarm.TaskState
		diagnostic  string
		code        faults.Code
		interrupted bool
		temporary   bool
	}{
		{state: swarm.TaskStatePending, diagnostic: "no suitable node (insufficient resources on 1 node)", code: faults.CodeNoSuitableNode, interrupted: true},
		{state: swarm.TaskStateRejected, diagnostic: "unauthorized: authentication required", code: faults.CodeImagePullDenied},
		{state: swarm.TaskStateRejected, diagnostic: `invalid mount config for type "bind": bind source path does not exist: /srv/data`, code: faults.CodeMountFailed},
		{state: swarm.TaskStateRejected, diagnostic: "No such image: api:2", code: faults.CodeImageNotFound},
		{state: swarm.TaskStateRejected, diagnostic: `failed to prepare extraction snapshot "extract-225092350-0LGf sha256:95dbfedee91178a3278e2e146a03bfd2223378ee9f60480b71df6f8d6cc1316e": NotFound: lease does not exist: not found`, code: faults.CodeImageExtractionFailed},
		{state: swarm.TaskStatePending, diagnostic: "waiting for assignment", interrupted: true},
		{state: swarm.TaskStateRejected, diagnostic: "image pull failed: context deadline exceeded"},
		{state: swarm.TaskStateRejected, diagnostic: "error from registry: unexpected response token=private-value", code: faults.CodeImagePullFailed, temporary: true},
		{state: swarm.TaskStateRejected, diagnostic: "error from registry: unexpected response password=private-value", code: faults.CodeImagePullFailed, interrupted: true, temporary: true},
		{state: swarm.TaskStateRejected, diagnostic: "error from registry: unsupported - token=private-value", code: faults.CodeImagePullFailed, interrupted: true},
	} {
		t.Run(tc.diagnostic, func(t *testing.T) {
			task := swarm.ServiceTask{CurrentState: tc.state, Error: tc.diagnostic,
				Spec: swarm.NewTaskSpec(dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:2"}})}
			var cause error = errors.New(task.Error)
			if fault := task.Fault(); fault != nil {
				cause = fault
			}
			cause = faults.SafeError(fmt.Errorf("verify service app/api: %w", cause))
			var event events.Event
			var typeName events.TypeName
			var wantErrorCode string
			if tc.interrupted {
				typeName, wantErrorCode = events.TypeNameDeployInterrupted, events.DeployInterruptReasonVerificationUnknown
				event = &events.DeployInterrupted{StackName: "app", Reason: wantErrorCode, Error: cause}
			} else {
				typeName, wantErrorCode = events.TypeNameDeployFailed, events.DeployErrorCodeConvergenceFailed
				event = &events.DeployFailed{
					DeployEvent: events.DeployEvent{StackName: "app"}, ErrorCode: wantErrorCode, Error: cause,
				}
			}
			// Use the durable event boundary that already preserves typed fault details.
			payload, err := codec.Encode(event)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), "private-value")
			decoded, err := codec.Decode(typeName, codec.Version, payload)
			require.NoError(t, err)
			actions, err := evaluator.Evaluate(t.Context(), events.Envelope{ID: "failure", Event: decoded})
			require.NoError(t, err)
			require.Len(t, actions, 1)
			action := actions[0]
			assert.Equal(t, wantErrorCode, action.Alert.ErrorCode)
			assert.Equal(t, cause.Error(), action.Alert.Message)
			assert.NotContains(t, action.Alert.Message, "private-value")
			wantOpenAfter := uint64(openImmediately)
			if tc.temporary {
				wantOpenAfter = temporaryFailureThreshold
			}
			assert.Equal(t, wantOpenAfter, action.OpenAfter)
			if tc.code == "" {
				assert.Empty(t, action.Findings)
				return
			}
			wantSummary := tc.diagnostic
			switch tc.code {
			case faults.CodeImageExtractionFailed:
				wantSummary = "Container image extraction failed: containerd snapshot lease is missing. Retry deployment; check for concurrent image cleanup."
			case faults.CodeImagePullFailed:
				wantSummary = "Docker could not pull the image."
				assert.Contains(t, action.Alert.Message, "=****")
			}
			assert.Equal(t, []model.Finding{{Code: tc.code, Summary: wantSummary}}, action.Findings)
		})
	}
}
