package tools

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

const diagnosticRemote = "https://robot:private-token@git.example.com/team/app.git"
const diagnosticIdentity = "git.example.com/team/app"
const diagnosticHead = "1111111111111111111111111111111111111111"

func expectLocalDiagnostics(t *testing.T, headErr, sizeErr, statusErr error) *gitx.MockRepository {
	t.Helper()
	repository := gitx.NewMockRepository(gomock.NewController(t))
	repository.EXPECT().Head(gomock.Any()).Return(diagnosticHead, headErr)
	repository.EXPECT().Branch(gomock.Any()).Return("", nil)
	repository.EXPECT().Shallow(gomock.Any()).Return(false, nil)
	if headErr == nil {
		repository.EXPECT().CountCommits(gomock.Any(), diagnosticHead).Return(3, nil)
	}
	repository.EXPECT().GitSize(gomock.Any()).Return(int64(64), sizeErr)
	repository.EXPECT().WorkingTreeSize(gomock.Any()).Return(int64(5), nil)
	repository.EXPECT().TrackedFiles(gomock.Any()).Return(1, nil)
	repository.EXPECT().Status(gomock.Any(), diagnosticUntrackedLimit).Return(gitx.WorktreeStatus{UntrackedFiles: []string{}, Dirty: false}, statusErr)
	return repository
}

func TestDiagnosticsJSONAndExecutorRegistration(t *testing.T) {
	db := testutil.OpenSQLite(t)
	state := modelstore.NewSQLStore(db)
	require.NoError(t, state.Update(t.Context(), func(runtime *model.Runtime) {
		runtime.GitAccess = &model.GitAccess{Repository: diagnosticIdentity, Available: true}
	}))
	repository := expectLocalDiagnostics(t, nil, nil, nil)
	executor := NewExecutor(&resources.Module{}, &gitops.Module{GitRepository: repository, Store: state}, nil,
		&alertmanagement.Module{Store: alertstore.NewSQLStore(db)}, &event.Module{}, &swarm.Swarm{}, nil, nil, nil, nil, nil,
		metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_repository_diagnostics"}).MCP, diagnosticRemote)
	var definition *routing.ToolDefinition
	for _, item := range executor.Definitions() {
		if item.Name == config.AssistantToolNameGitRepositoryDiagnostics {
			copy := item
			definition = &copy
		}
	}
	require.NotNil(t, definition)
	assert.Equal(t, false, definition.ParametersJSONSchema["additionalProperties"])
	assert.Empty(t, definition.ParametersJSONSchema["properties"])
	response, err := executor.Execute(t.Context(), routing.Request{ToolName: "git_repository_diagnostics", Payload: `{}`})
	require.NoError(t, err)
	assert.JSONEq(t, `{"head":"1111111111111111111111111111111111111111","branch":null,"shallow":false,"commits":3,
 "gitSizeBytes":64,"workingTreeSizeBytes":5,"trackedFiles":1,"workingTreeDirty":false,"untrackedFiles":[],
 "untrackedFilesTruncated":false,"availability":true,"alertId":null,"errors":{}}`, response)
	assert.NotContains(t, response, "private-token")
}

func TestDiagnosticsAvailabilityAndAlertMatching(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name      string
		access    *model.GitAccess
		failure   *model.GitPullFailure
		open      bool
		want      *bool
		wantAlert bool
	}{
		{name: "confirmed success", access: &model.GitAccess{Repository: diagnosticIdentity, Available: true}, want: &yes},
		{name: "failure with alert", access: &model.GitAccess{Repository: diagnosticIdentity}, open: true, want: &no, wantAlert: true},
		{name: "failure below alert threshold", access: &model.GitAccess{Repository: diagnosticIdentity}, want: &no},
		{name: "initial unknown"},
		{name: "legacy failure is not remote proof", failure: &model.GitPullFailure{Repository: diagnosticIdentity}},
		{name: "old repository observation", access: &model.GitAccess{Repository: "git.example.com/old", Available: true}},
		{name: "pending alert recovery", access: &model.GitAccess{Repository: diagnosticIdentity, Available: true}, open: true, want: &yes, wantAlert: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.OpenSQLite(t)
			store := modelstore.NewSQLStore(db)
			alerts := alertstore.NewSQLStore(db)
			require.NoError(t, store.Update(t.Context(), func(runtime *model.Runtime) { runtime.GitAccess = tc.access; runtime.GitPullFailure = tc.failure }))
			now := time.Now()
			// A newer incident for another repository must never leak into the response.
			for _, identity := range []string{diagnosticIdentity, "git.example.com/other"} {
				status := alertmodel.AlertStatusOpen
				if identity == diagnosticIdentity && !tc.open {
					status = alertmodel.AlertStatusResolved
				}
				alert := alertmodel.Alert{ID: identity, Fingerprint: alertmodel.GitRepositoryUnavailableFingerprint(identity),
					Kind: alertmodel.AlertKindGitRepositoryUnavailable, ResourceType: alertmodel.ResourceTypeRepository, ResourceID: identity,
					Status: status, Title: "Git unavailable", OpenedAt: now, UpdatedAt: now, Occurrences: 1}
				if status == alertmodel.AlertStatusResolved {
					alert.ResolvedAt = &now
				}
				require.NoError(t, alerts.Create(t.Context(), alert))
			}
			tool := NewGitRepositoryDiagnostics(expectLocalDiagnostics(t, nil, nil, nil), store, alerts, diagnosticRemote)
			response, err := tool.Execute(t.Context(), routing.Request{})
			require.NoError(t, err)
			result := response.Payload.(repositoryDiagnosticsResponse)
			assert.Equal(t, tc.want, result.Availability)
			if tc.wantAlert {
				require.NotNil(t, result.AlertID)
				assert.Equal(t, diagnosticIdentity, *result.AlertID)
			} else {
				assert.Nil(t, result.AlertID)
			}
			assert.Empty(t, result.Errors)
		})
	}
}

func TestDiagnosticsPartialResults(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		headErr, sizeErr, statusErr error
	}{
		{name: "head", headErr: errors.New("private-token")},
		{name: "size budget", sizeErr: gitx.ErrScanLimit},
		{name: "status deadline", statusErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.OpenSQLite(t)
			tool := NewGitRepositoryDiagnostics(expectLocalDiagnostics(t, tc.headErr, tc.sizeErr, tc.statusErr),
				modelstore.NewSQLStore(db), alertstore.NewSQLStore(db), diagnosticRemote)
			response, err := tool.Execute(t.Context(), routing.Request{})
			require.NoError(t, err)
			result := response.Payload.(repositoryDiagnosticsResponse)
			assert.Equal(t, int64(5), *result.WorkingTreeSizeBytes)
			assert.Equal(t, 1, *result.TrackedFiles)
			if tc.headErr != nil {
				assert.Nil(t, result.Head)
				assert.Nil(t, result.Commits)
				assert.Equal(t, "head_unavailable", result.Errors["commits"])
			}
			if tc.sizeErr != nil {
				assert.Nil(t, result.GitSizeBytes)
				assert.Equal(t, "scan_limit_exceeded", result.Errors["gitSizeBytes"])
			}
			if tc.statusErr != nil {
				assert.Nil(t, result.WorkingTreeDirty)
				assert.Nil(t, result.UntrackedFiles)
				assert.Nil(t, result.UntrackedFilesTruncated)
				assert.Equal(t, "deadline_exceeded", result.Errors["untrackedFiles"])
			}
			encoded, err := json.Marshal(result, jsonv1.DefaultOptionsV1())
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-token")
		})
	}
}

func TestDiagnosticsStateFailureDoesNotHideLocalMetrics(t *testing.T) {
	db := testutil.OpenSQLite(t)
	// Cancellation is honored by SQL reads. Local mock observations remain independently available.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tool := NewGitRepositoryDiagnostics(expectLocalDiagnostics(t, nil, nil, nil), modelstore.NewSQLStore(db), alertstore.NewSQLStore(db), diagnosticRemote)
	response, err := tool.Execute(ctx, routing.Request{})
	require.NoError(t, err)
	result := response.Payload.(repositoryDiagnosticsResponse)
	assert.Nil(t, result.Availability)
	assert.Nil(t, result.AlertID)
	assert.Equal(t, "canceled", result.Errors["availability"])
	assert.Equal(t, "canceled", result.Errors["alertId"])
	assert.Equal(t, diagnosticHead, *result.Head)
}
