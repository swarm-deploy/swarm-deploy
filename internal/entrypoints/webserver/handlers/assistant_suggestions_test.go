package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestAssistantSuggestionsReturnsAllSourcesInDeterministicOrder(t *testing.T) {
	deploymentDB := testutil.OpenSQLite(t)
	createSuccessfulDeployment(t, deploymentDB, "monitoring", assistantCompose("ip-scanner"), "deploy-1")
	alerts := alertstore.NewSQLStore(testutil.OpenSQLite(t))
	openedAt := time.Date(2026, time.October, 21, 17, 30, 0, 0, time.FixedZone("offset", 2*60*60))
	resolvedAt := openedAt.Add(time.Hour)
	require.NoError(t, alerts.Create(t.Context(), alertmodel.Alert{
		ID: "123", Fingerprint: "sync_failed:stack:auth", Kind: alertmodel.AlertKindSyncFailed,
		ResourceType: alertmodel.ResourceTypeStack, ResourceID: "auth", Status: alertmodel.AlertStatusResolved,
		Title: "do not expose this title", Message: "password=do-not-expose", DeploymentID: "deployment-123",
		FailureStage: "apply", ErrorCode: "apply_failed", Commit: "abc123", Occurrences: 1,
		OpenedAt: openedAt, UpdatedAt: resolvedAt, ResolvedAt: &resolvedAt,
	}))

	response, body := requestAssistantSuggestions(t, &handler{
		deployments: deploymentstore.NewStore(deploymentDB),
		alerts:      alerts,
	})

	require.Len(t, response.Suggestions, 4)
	assert.Equal(t, []string{
		"latest-service-logs", "latest-alert", "cluster-state", "today-deployments",
	}, suggestionIDs(response.Suggestions))
	assert.Equal(t, "Analyze logs of service ip-scanner", response.Suggestions[0].Title)
	assert.Contains(t, response.Suggestions[0].Prompt, "service ip-scanner in stack monitoring")
	assert.Equal(t, "Why did the deployment of auth fail?", response.Suggestions[1].Title)
	assert.Contains(t, response.Suggestions[1].Prompt, "- Alert ID: 123")
	assert.Contains(t, response.Suggestions[1].Prompt, "- Status: resolved")
	assert.Contains(t, response.Suggestions[1].Prompt, "- Stack: auth")
	assert.Contains(t, response.Suggestions[1].Prompt, "- Created at: 2026-10-21T15:30:00Z")
	assert.Contains(t, response.Suggestions[1].Prompt, "Verify the root cause against actual deployment and cluster data.")
	assert.NotContains(t, string(body), "do-not-expose")
	assert.NotContains(t, string(body), "do not expose this title")
}

func TestAssistantSuggestionsUsesLatestActuallyChangedService(t *testing.T) {
	db := testutil.OpenSQLite(t)
	createSuccessfulDeployment(t, db, "app", assistantCompose("api"), "baseline")
	withWorker := compose.File{Compose: compose.Compose{Services: compose.Services{
		{Name: "api", Image: "api:1"},
		{Name: "worker", Image: "worker:1"},
	}}}
	createSuccessfulDeployment(t, db, "app", withWorker, "worker")
	withNetwork := withWorker
	withNetwork.Compose.Networks = map[string]compose.Network{"frontend": {Name: "frontend"}}
	createSuccessfulDeployment(t, db, "app", withNetwork, "network-only")

	response, _ := requestAssistantSuggestions(t, &handler{
		deployments: deploymentstore.NewStore(db),
		alerts:      alertstore.NewSQLStore(testutil.OpenSQLite(t)),
	})

	require.Len(t, response.Suggestions, 3)
	assert.Equal(t, "latest-service-logs", response.Suggestions[0].ID)
	assert.Equal(t, "Analyze logs of service worker", response.Suggestions[0].Title)
	assert.Contains(t, response.Suggestions[0].Prompt, "in stack app")
}

func TestAssistantSuggestionsSelectsLatestAlertByCreationTime(t *testing.T) {
	alerts := alertstore.NewSQLStore(testutil.OpenSQLite(t))
	base := time.Date(2026, time.October, 21, 10, 0, 0, 0, time.UTC)
	require.NoError(t, alerts.Create(t.Context(), alertmodel.Alert{
		ID: "older", Fingerprint: "older", Kind: alertmodel.AlertKindNodeDisconnected,
		ResourceType: alertmodel.ResourceTypeNode, ResourceID: "worker-old", Status: alertmodel.AlertStatusOpen,
		OpenedAt: base, UpdatedAt: base.Add(3 * time.Hour),
	}))
	closedAt := base.Add(2 * time.Hour)
	require.NoError(t, alerts.Create(t.Context(), alertmodel.Alert{
		ID: "newer", Fingerprint: "newer", Kind: alertmodel.AlertKindNodeDisconnected,
		ResourceType: alertmodel.ResourceTypeNode, ResourceID: "worker-2", Status: alertmodel.AlertStatusResolved,
		OpenedAt: base.Add(time.Hour), UpdatedAt: closedAt, ResolvedAt: &closedAt,
	}))

	response, _ := requestAssistantSuggestions(t, &handler{
		deployments: deploymentstore.NewStore(testutil.OpenSQLite(t)),
		alerts:      alerts,
	})

	require.Len(t, response.Suggestions, 3)
	assert.Equal(t, "latest-alert", response.Suggestions[0].ID)
	assert.Equal(t, "Why is node worker-2 unavailable?", response.Suggestions[0].Title)
	assert.Contains(t, response.Suggestions[0].Prompt, "- Status: resolved")
}

func TestAlertSuggestionQuestionTemplates(t *testing.T) {
	createdAt := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name         string
		kind         alertmodel.AlertKind
		resourceType alertmodel.ResourceType
		resourceID   string
		want         string
	}{
		{name: "deployment", kind: alertmodel.AlertKindSyncFailed, resourceType: alertmodel.ResourceTypeStack,
			resourceID: "payments", want: "Why did the deployment of payments fail?"},
		{name: "git", kind: alertmodel.AlertKindGitRepositoryUnavailable,
			resourceType: alertmodel.ResourceTypeRepository, resourceID: "origin", want: "Why is Git connectivity unavailable?"},
		{name: "node", kind: alertmodel.AlertKindNodeDisconnected, resourceType: alertmodel.ResourceTypeNode,
			resourceID: "worker-2", want: "Why is node worker-2 unavailable?"},
		{name: "unknown", kind: alertmodel.AlertKind("future_kind"), resourceType: alertmodel.ResourceType("service"),
			resourceID: "nginx", want: "What caused the alert for service nginx?"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			suggestion, ok := assistantAlertSuggestion(alertmodel.Alert{
				ID: "alert-id", Kind: testCase.kind, ResourceType: testCase.resourceType,
				ResourceID: testCase.resourceID, OpenedAt: createdAt,
			})
			require.True(t, ok)
			assert.Equal(t, testCase.want, suggestion.Title)
			assert.Equal(t, testCase.want, suggestion.Prompt[:len(testCase.want)])
		})
	}
}

func TestAlertSuggestionOmitsUnavailableOptionalContext(t *testing.T) {
	suggestion, ok := assistantAlertSuggestion(alertmodel.Alert{
		ID: "alert-id", Kind: alertmodel.AlertKind("future_kind"),
		OpenedAt: time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC),
		Message:  "raw stack trace must not be exposed",
	})
	require.True(t, ok)
	assert.NotContains(t, suggestion.Prompt, "Resource ID:")
	assert.NotContains(t, suggestion.Prompt, "Deployment ID:")
	assert.NotContains(t, suggestion.Prompt, "Failure stage:")
	assert.NotContains(t, suggestion.Prompt, "raw stack trace")
}

func TestAssistantSuggestionsDynamicFailuresRemainHTTP200(t *testing.T) {
	testCases := []struct {
		name                  string
		deploymentUnavailable bool
		alertUnavailable      bool
		withDeployment        bool
		withAlert             bool
		wantIDs               []string
	}{
		{
			name: "deployment repository error", deploymentUnavailable: true, withAlert: true,
			wantIDs: []string{"latest-alert", "cluster-state", "today-deployments"},
		},
		{
			name: "alert repository error", alertUnavailable: true, withDeployment: true,
			wantIDs: []string{"latest-service-logs", "cluster-state", "today-deployments"},
		},
		{
			name: "both repositories error", deploymentUnavailable: true, alertUnavailable: true,
			wantIDs: []string{"cluster-state", "today-deployments"},
		},
		{
			name:    "no dynamic records",
			wantIDs: []string{"cluster-state", "today-deployments"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			deploymentDB := openSuggestionDatabase(t)
			if testCase.withDeployment {
				createSuccessfulDeployment(t, deploymentDB, "app", assistantCompose("api"), "deploy")
			}
			alertDB := openSuggestionDatabase(t)
			alerts := alertstore.NewSQLStore(alertDB)
			if testCase.withAlert {
				require.NoError(t, alerts.Create(t.Context(), alertmodel.Alert{
					ID: "alert", Fingerprint: "alert", Kind: alertmodel.AlertKindGitRepositoryUnavailable,
					ResourceType: alertmodel.ResourceTypeRepository, ResourceID: "origin",
					Status: alertmodel.AlertStatusOpen, OpenedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
				}))
			}
			if testCase.deploymentUnavailable {
				require.NoError(t, deploymentDB.Close())
			} else {
				t.Cleanup(func() { require.NoError(t, deploymentDB.Close()) })
			}
			if testCase.alertUnavailable {
				require.NoError(t, alertDB.Close())
			} else {
				t.Cleanup(func() { require.NoError(t, alertDB.Close()) })
			}

			var logs bytes.Buffer
			previousLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previousLogger) })
			response, body := requestAssistantSuggestions(t, &handler{
				deployments: deploymentstore.NewStore(deploymentDB),
				alerts:      alerts,
			})

			assert.Equal(t, testCase.wantIDs, suggestionIDs(response.Suggestions))
			assert.NotContains(t, string(body), "database is closed")
			if testCase.deploymentUnavailable {
				assert.Contains(t, logs.String(), "failed to retrieve assistant deployment suggestion")
			}
			if testCase.alertUnavailable {
				assert.Contains(t, logs.String(), "failed to retrieve assistant alert suggestion")
			}
		})
	}
}

func assistantCompose(serviceName string) compose.File {
	return compose.File{Compose: compose.Compose{Services: compose.Services{{Name: serviceName, Image: serviceName + ":1"}}}}
}

func createSuccessfulDeployment(
	t *testing.T,
	db storage.Database,
	stack string,
	file compose.File,
	commit string,
) {
	t.Helper()
	publisher := eventbus.NewOutboxDispatcher(outbox.New(db))
	service := deployment.NewService(db, publisher)
	attempt, err := deployments.NewService(db, publisher).StartIfChanged(
		t.Context(), stack, commit, file, true, service.ProjectInterruption,
	)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	require.NoError(t, service.Succeed(t.Context(), attempt.ID, gitopsmodel.Stack{}))
}

func openSuggestionDatabase(t *testing.T) storage.Database {
	t.Helper()
	db, err := storage.OpenSQLite(context.Background(), t.TempDir())
	require.NoError(t, err)
	return db
}

func requestAssistantSuggestions(
	t *testing.T,
	h *handler,
) (generated.AssistantSuggestionsResponse, []byte) {
	t.Helper()
	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/assistant/suggestions", nil)
	server.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response generated.AssistantSuggestionsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NoError(t, response.Validate())
	return response, recorder.Body.Bytes()
}

func suggestionIDs(suggestions []generated.AssistantSuggestion) []string {
	ids := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		ids = append(ids, suggestion.ID)
	}
	return ids
}
