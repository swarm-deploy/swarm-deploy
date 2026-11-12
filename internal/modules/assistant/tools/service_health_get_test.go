package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

type serviceHealthTestStores struct {
	alerts      *alertstore.SQLStore
	deployments *deploymentstore.Store
	service     *deployments.Service
}

func newServiceHealthTestStores(t *testing.T) serviceHealthTestStores {
	t.Helper()
	db := testutil.OpenSQLite(t)
	clock := serviceHealthTestNow.Add(-time.Hour)

	return serviceHealthTestStores{
		alerts:      alertstore.NewSQLStore(db),
		deployments: deploymentstore.NewStore(db),
		service: deployments.NewService(db, &dispatcher.NopDispatcher{}, deployments.WithClock(func() time.Time {
			clock = clock.Add(time.Minute)
			return clock
		})),
	}
}

func (s serviceHealthTestStores) deploy(t *testing.T, index int, outcome deployments.CompletionOutcome) string {
	t.Helper()
	attempt, err := s.service.StartIfChanged(
		t.Context(), "shop", string(rune('a'+index)), deploymentToolCompose(index), true,
	)
	require.NoError(t, err)
	completion := deployments.Completion{Outcome: outcome}
	if outcome != deployments.CompletionSucceeded {
		completion.Diagnostic = errors.New("apply failed")
	}
	require.NoError(t, s.service.Complete(t.Context(), attempt.ID, completion))

	return attempt.DeploymentID
}

func newServiceHealthTestTool(
	t *testing.T,
	services swarm.ServiceManager,
	nodes NodesReader,
	stores serviceHealthTestStores,
) *ServiceHealthGet {
	t.Helper()
	tool := NewServiceHealthGet(services, nodes, stores.alerts, stores.deployments)
	tool.now = func() time.Time { return serviceHealthTestNow }

	return tool
}

func executeServiceHealth(ctx context.Context, t *testing.T, tool *ServiceHealthGet) serviceHealthResponse {
	t.Helper()
	response, err := tool.Execute(ctx, routing.Request{Payload: serviceHealthGetRequest{
		StackName: " shop ", ServiceName: " api ",
	}})
	require.NoError(t, err)
	payload, ok := response.Payload.(serviceHealthResponse)
	require.True(t, ok)

	return payload
}

func expectServiceHealthLiveAPI(services *swarm.MockServiceManager, live *swarm.StackService) {
	other := *live
	other.ID, other.Name, other.FullName = "svc-worker", "worker", "shop_worker"
	services.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "shop"}).
		Return([]swarm.StackService{other, *live}, nil)
	services.EXPECT().ListTasks(gomock.Any(), "svc-api").Return([]swarm.ServiceTask{
		serviceHealthTestTask("r1", 1, live.ServiceSpec.TaskTemplate, swarm.TaskStateRunning, time.Hour),
	}, nil)
}

func TestServiceHealthGetCollectsAndLinksEvidence(t *testing.T) {
	stores := newServiceHealthTestStores(t)
	succeededID := stores.deploy(t, 0, deployments.CompletionSucceeded)
	failedID := stores.deploy(t, 1, deployments.CompletionApplyFailed)

	openedAt := serviceHealthTestNow.Add(-time.Hour)
	resolvedAt := openedAt.Add(time.Minute)
	alerts := []alertmodel.Alert{
		{ID: "drift", Fingerprint: "service_drift:service:shop/api", Kind: alertmodel.AlertKindServiceDrift,
			ResourceType: alertmodel.ResourceTypeService, ResourceID: "shop/api", Status: alertmodel.AlertStatusOpen,
			Title: "Service drifted", Occurrences: 1, OpenedAt: openedAt, UpdatedAt: openedAt},
		{ID: "linked", Fingerprint: "sync_failed:stack:shop:linked", Kind: alertmodel.AlertKindSyncFailed,
			ResourceType: alertmodel.ResourceTypeStack, ResourceID: "shop", Status: alertmodel.AlertStatusOpen,
			Title: "Synchronization failed", DeploymentID: failedID, Occurrences: 2,
			Findings: []alertmodel.Finding{{Code: faults.CodeImageNotFound, Summary: "api:b is missing",
				DetectedAt: openedAt}},
			OpenedAt: openedAt, UpdatedAt: openedAt.Add(time.Minute)},
		{ID: "unrelated", Fingerprint: "sync_failed:stack:shop:unrelated", Kind: alertmodel.AlertKindSyncFailed,
			ResourceType: alertmodel.ResourceTypeStack, ResourceID: "shop", Status: alertmodel.AlertStatusOpen,
			Title: "Synchronization failed", DeploymentID: "deployment-of-another-service", Occurrences: 1,
			OpenedAt: openedAt, UpdatedAt: openedAt},
		{ID: "preparation", Fingerprint: "sync_failed:stack:shop:preparation", Kind: alertmodel.AlertKindSyncFailed,
			ResourceType: alertmodel.ResourceTypeStack, ResourceID: "shop", Status: alertmodel.AlertStatusOpen,
			Title: "Synchronization failed", Occurrences: 1,
			OpenedAt: openedAt, UpdatedAt: openedAt.Add(-time.Minute)},
		{ID: "resolved", Fingerprint: "service_drift:service:shop/api:resolved", Kind: alertmodel.AlertKindServiceDrift,
			ResourceType: alertmodel.ResourceTypeService, ResourceID: "shop/api", Status: alertmodel.AlertStatusResolved,
			Title: "Service drifted", Occurrences: 1, OpenedAt: openedAt, UpdatedAt: resolvedAt, ResolvedAt: &resolvedAt,
			Resolution: &alertmodel.AlertResolution{Reason: alertmodel.ResolutionReasonRecovered, Message: "recovered"}},
	}
	for _, alert := range alerts {
		require.NoError(t, stores.alerts.Create(t.Context(), alert))
	}

	ctrl := gomock.NewController(t)
	services := swarm.NewMockServiceManager(ctrl)
	expectServiceHealthLiveAPI(services, serviceHealthTestLive(1, time.Hour))
	tool := newServiceHealthTestTool(t, services, nodeStore(t, []swarm.Node{{ID: "node-1", Status: "ready"}}), stores)

	payload := executeServiceHealth(t.Context(), t, tool)

	assert.Equal(t, "shop", payload.StackName)
	assert.Equal(t, "api", payload.ServiceName)
	assert.Equal(t, serviceHealthSources{
		Swarm: serviceHealthSourceOK, Nodes: serviceHealthSourceOK,
		Alerts: serviceHealthSourceOK, Deployments: serviceHealthSourceOK,
	}, payload.Sources)
	assert.Equal(t, serviceHealthStatusDegraded, payload.Status)
	require.NotNil(t, payload.Service)
	assert.Equal(t, uint64(1), payload.Service.Running)

	alertIDs := make([]string, 0, len(payload.Alerts))
	linked := map[string]bool{}
	for _, alert := range payload.Alerts {
		alertIDs = append(alertIDs, alert.ID)
		linked[alert.ID] = alert.Linked
	}
	assert.Equal(t, []string{"linked", "drift", "preparation"}, alertIDs,
		"open alerts only, newest update first, unrelated stack alert dropped")
	assert.True(t, linked["linked"])
	assert.True(t, linked["drift"])
	assert.False(t, linked["preparation"])
	require.Len(t, payload.Alerts[0].Findings, 1)
	assert.Equal(t, faults.CodeImageNotFound, payload.Alerts[0].Findings[0].Code)

	require.Len(t, payload.Deployments, 2)
	assert.Equal(t, failedID, payload.Deployments[0].ID)
	assert.Equal(t, serviceHealthDeploymentRoleLatest, payload.Deployments[0].Role)
	assert.Equal(t, succeededID, payload.Deployments[1].ID)
	assert.Equal(t, serviceHealthDeploymentRoleLastSucceeded, payload.Deployments[1].Role)

	failedDeploy := findServiceHealthFlag(t, payload.Flags, serviceHealthFlagLastDeployFailed)
	assert.Equal(t, serviceHealthSeverityWarning, failedDeploy.Severity, "the service still runs")
	assert.Equal(t, failedID, failedDeploy.Refs.DeploymentID)
	findServiceHealthFlag(t, payload.Flags, serviceHealthFlagDriftDetected)
}

func TestServiceHealthGetSourceAvailability(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	testCases := []struct {
		name          string
		ctx           context.Context
		setup         func(*gomock.Controller) (swarm.ServiceManager, NodesReader)
		expectStatus  string
		expectSources serviceHealthSources
		expectService bool
	}{
		{
			name: "optional sources unavailable",
			ctx:  canceled,
			setup: func(ctrl *gomock.Controller) (swarm.ServiceManager, NodesReader) {
				services := swarm.NewMockServiceManager(ctrl)
				expectServiceHealthLiveAPI(services, serviceHealthTestLive(1, time.Hour))
				nodes := NewMockNodesReader(ctrl)
				nodes.EXPECT().ReadAll(gomock.Any()).Return(nil, errors.New("nodes unavailable"))
				return services, nodes
			},
			expectStatus: serviceHealthStatusHealthy,
			expectSources: serviceHealthSources{
				Swarm: serviceHealthSourceOK, Nodes: serviceHealthSourceUnavailable,
				Alerts: serviceHealthSourceUnavailable, Deployments: serviceHealthSourceUnavailable,
			},
			expectService: true,
		},
		{
			name: "swarm unavailable",
			ctx:  t.Context(),
			setup: func(ctrl *gomock.Controller) (swarm.ServiceManager, NodesReader) {
				services := swarm.NewMockServiceManager(ctrl)
				services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, errors.New("docker unavailable"))
				return services, NewMockNodesReader(ctrl)
			},
			expectStatus: serviceHealthStatusUnknown,
			expectSources: serviceHealthSources{
				Swarm: serviceHealthSourceUnavailable, Nodes: serviceHealthSourceSkipped,
				Alerts: serviceHealthSourceOK, Deployments: serviceHealthSourceOK,
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			services, nodes := testCase.setup(gomock.NewController(t))
			tool := newServiceHealthTestTool(t, services, nodes, newServiceHealthTestStores(t))

			payload := executeServiceHealth(testCase.ctx, t, tool)

			assert.Equal(t, testCase.expectStatus, payload.Status)
			assert.Equal(t, testCase.expectSources, payload.Sources)
			assert.Equal(t, testCase.expectService, payload.Service != nil)
		})
	}
}

func TestServiceHealthGetErrors(t *testing.T) {
	testCases := []struct {
		name    string
		request serviceHealthGetRequest
		setup   func(*swarm.MockServiceManager)
		message string
	}{
		{
			name:    "missing stack",
			request: serviceHealthGetRequest{ServiceName: "api"},
			message: "stack_name is required",
		},
		{
			name:    "missing service",
			request: serviceHealthGetRequest{StackName: "shop"},
			message: "service_name is required",
		},
		{
			name:    "unknown service",
			request: serviceHealthGetRequest{StackName: "shop", ServiceName: "api"},
			setup: func(services *swarm.MockServiceManager) {
				services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{}, nil)
			},
			message: `service "api" not found in stack "shop": no live service and no deployment contains it`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			services := swarm.NewMockServiceManager(ctrl)
			if testCase.setup != nil {
				testCase.setup(services)
			}
			tool := newServiceHealthTestTool(t, services, NewMockNodesReader(ctrl), newServiceHealthTestStores(t))

			_, err := tool.Execute(t.Context(), routing.Request{Payload: testCase.request})

			require.EqualError(t, err, testCase.message)
		})
	}
}
