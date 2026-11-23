package evaluators

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestEvaluatorsUpdateOpenFailureWithoutReplacingIncident(t *testing.T) {
	store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	for _, tc := range []struct {
		name        string
		evaluator   Evaluator
		event       events.Event
		fingerprint string
		kind        model.AlertKind
		resource    model.ResourceType
		resourceID  string
		openAfter   uint64
		trackStreak bool
		message     string
		commit      string
		code        string
	}{
		{
			name: "Git network failure updates diagnostics", evaluator: NewGitRepositoryEvaluator(store),
			event:       &events.GitPullFailed{Repository: "repo", Category: events.GitFailureNetwork, Description: "network down"},
			fingerprint: model.GitRepositoryUnavailableFingerprint("repo"), kind: model.AlertKindGitRepositoryUnavailable,
			resource: model.ResourceTypeRepository, resourceID: "repo", openAfter: 3, trackStreak: true,
			message: "network down", commit: "previous-commit", code: string(events.GitFailureNetwork),
		},
		{
			name: "node disconnection keeps existing diagnostics", evaluator: NewNodeEvaluator(store),
			event:       &events.NodeDisconnected{NodeID: "worker"},
			fingerprint: model.NodeDisconnectedFingerprint("worker"), kind: model.AlertKindNodeDisconnected,
			resource: model.ResourceTypeNode, resourceID: "worker", openAfter: 1,
			message: "previous detail", commit: "previous-commit", code: "previous-code",
		},
		{
			name: "service drift updates reason and commit", evaluator: NewServiceDriftEvaluator(store),
			event:       &events.ServiceDrifted{StackName: "app", ServiceName: "api", Reason: "replica count differs", Commit: "new-commit"},
			fingerprint: model.ServiceDriftFingerprint("app", "api"), kind: model.AlertKindServiceDrift,
			resource: model.ResourceTypeService, resourceID: "app/api", openAfter: 1,
			message: "replica count differs", commit: "new-commit", code: "previous-code",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			at := time.Now().UTC()
			original := model.Alert{
				ID: tc.fingerprint, Fingerprint: tc.fingerprint, Kind: tc.kind, ResourceType: tc.resource, ResourceID: tc.resourceID,
				Status: model.AlertStatusOpen, Title: "previous title", Message: "previous detail", Commit: "previous-commit",
				FailureStage: "previous stage", ErrorCode: "previous-code", OpenEventID: "open", LatestEventID: "open",
				OpenedAt: at, UpdatedAt: at, Occurrences: 2,
			}
			require.NoError(t, store.Create(ctx, original))

			actions, err := tc.evaluator.Evaluate(ctx, events.Envelope{ID: "repeat", Event: tc.event})
			require.NoError(t, err)
			require.Len(t, actions, 1)
			want := original
			want.Message, want.Commit, want.ErrorCode, want.LatestEventID = tc.message, tc.commit, tc.code, "repeat"
			assert.Equal(t, model.AlertActionRecordFailure, actions[0].Kind)
			assert.Equal(t, tc.openAfter, actions[0].OpenAfter)
			assert.Equal(t, tc.trackStreak, actions[0].TrackFailureStreak)
			assert.Equal(t, want, actions[0].Alert)
		})
	}
}
