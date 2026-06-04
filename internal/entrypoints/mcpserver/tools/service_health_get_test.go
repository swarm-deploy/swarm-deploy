package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestGetServiceHealthExecute(t *testing.T) {
	t.Parallel()

	type responsePayload struct {
		StackName          string                     `json:"stack_name"`
		ServiceName        string                     `json:"service_name"`
		Mode               string                     `json:"mode"`
		DesiredReplicas    *uint64                    `json:"desired_replicas"`
		HealthStatus       string                     `json:"health_status"`
		RunningTasks       int                        `json:"running_tasks"`
		ProgressingTasks   int                        `json:"progressing_tasks"`
		TerminalErrorTasks int                        `json:"terminal_error_tasks"`
		TotalTasks         int                        `json:"total_tasks"`
		StateCounts        map[string]int             `json:"state_counts"`
		UpdateStatus       *swarm.ServiceUpdateStatus `json:"update_status"`
		Tasks              []swarm.ServiceTask        `json:"tasks"`
	}

	testCases := []struct {
		name                 string
		service              swarm.Service
		tasks                []swarm.ServiceTask
		expectedMode         string
		expectedDesired      *uint64
		expectedHealth       string
		expectedRunning      int
		expectedProgressing  int
		expectedTerminal     int
		expectedTotal        int
		expectedStateCounts  map[string]int
		expectedUpdateStatus *swarm.ServiceUpdateStatus
	}{
		{
			name: "healthy replicated service",
			service: swarm.Service{
				Spec: swarm.ServiceSpec{
					Mode:     "replicated",
					Replicas: 2,
				},
				UpdateStatus: &swarm.ServiceUpdateStatus{
					State: "completed",
				},
			},
			tasks: []swarm.ServiceTask{
				{ID: "task-1", CurrentState: "running"},
				{ID: "task-2", CurrentState: "running"},
			},
			expectedMode:    "replicated",
			expectedDesired: uint64Pointer(2),
			expectedHealth:  "healthy",
			expectedRunning: 2,
			expectedTotal:   2,
			expectedStateCounts: map[string]int{
				"running": 2,
			},
			expectedUpdateStatus: &swarm.ServiceUpdateStatus{
				State: "completed",
			},
		},
		{
			name: "updating replicated service",
			service: swarm.Service{
				Spec: swarm.ServiceSpec{
					Mode:     "replicated",
					Replicas: 3,
				},
				UpdateStatus: &swarm.ServiceUpdateStatus{
					State: "updating",
				},
			},
			tasks: []swarm.ServiceTask{
				{ID: "task-1", CurrentState: "running"},
				{ID: "task-2", CurrentState: "starting"},
			},
			expectedMode:        "replicated",
			expectedDesired:     uint64Pointer(3),
			expectedHealth:      "updating",
			expectedRunning:     1,
			expectedProgressing: 1,
			expectedTotal:       2,
			expectedStateCounts: map[string]int{
				"running":  1,
				"starting": 1,
			},
			expectedUpdateStatus: &swarm.ServiceUpdateStatus{
				State: "updating",
			},
		},
		{
			name: "failed service without running tasks",
			service: swarm.Service{
				Spec: swarm.ServiceSpec{
					Mode:     "replicated",
					Replicas: 1,
				},
			},
			tasks: []swarm.ServiceTask{
				{ID: "task-1", CurrentState: "failed", Error: "crash loop"},
				{ID: "task-2", CurrentState: "rejected", Error: "invalid image"},
			},
			expectedMode:     "replicated",
			expectedDesired:  uint64Pointer(1),
			expectedHealth:   "failed",
			expectedTerminal: 2,
			expectedTotal:    2,
			expectedStateCounts: map[string]int{
				"failed":   1,
				"rejected": 1,
			},
		},
		{
			name: "scaled to zero service",
			service: swarm.Service{
				Spec: swarm.ServiceSpec{
					Mode:     "replicated",
					Replicas: 0,
				},
			},
			expectedMode:        "replicated",
			expectedDesired:     uint64Pointer(0),
			expectedHealth:      "scaled_to_zero",
			expectedStateCounts: map[string]int{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			inspector := swarm.NewMockServiceManager(ctrl)
			tool := NewGetServiceHealth(inspector)

			serviceRef := swarm.NewServiceReference("core", "api")
			inspector.EXPECT().
				Get(gomock.Any(), serviceRef).
				Return(testCase.service, nil)
			inspector.EXPECT().
				ListTasks(gomock.Any(), serviceRef).
				Return(testCase.tasks, nil)

			response, err := tool.Execute(context.Background(), routing.Request{
				Payload: getServiceHealthRequest{
					StackName:   "core",
					ServiceName: "api",
				},
			})
			require.NoError(t, err, "execute service_health_get")

			var payload responsePayload
			encoded, err := json.Marshal(response.Payload)
			require.NoError(t, err, "encode response payload")
			require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

			assert.Equal(t, "core", payload.StackName, "unexpected stack name")
			assert.Equal(t, "api", payload.ServiceName, "unexpected service name")
			assert.Equal(t, testCase.expectedMode, payload.Mode, "unexpected mode")
			assert.Equal(t, testCase.expectedDesired, payload.DesiredReplicas, "unexpected desired replicas")
			assert.Equal(t, testCase.expectedHealth, payload.HealthStatus, "unexpected health status")
			assert.Equal(t, testCase.expectedRunning, payload.RunningTasks, "unexpected running count")
			assert.Equal(t, testCase.expectedProgressing, payload.ProgressingTasks, "unexpected progressing count")
			assert.Equal(t, testCase.expectedTerminal, payload.TerminalErrorTasks, "unexpected terminal errors count")
			assert.Equal(t, testCase.expectedTotal, payload.TotalTasks, "unexpected total tasks count")
			assert.Equal(t, testCase.expectedStateCounts, payload.StateCounts, "unexpected state counts")
			assert.Equal(t, testCase.expectedUpdateStatus, payload.UpdateStatus, "unexpected update status")
			assert.Equal(t, testCase.tasks, payload.Tasks, "unexpected raw tasks")
		})
	}
}

func TestGetServiceHealthExecuteErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		request     getServiceHealthRequest
		prepareMock func(*swarm.MockServiceManager)
		assertError func(*testing.T, error)
	}{
		{
			name: "missing stack name",
			request: getServiceHealthRequest{
				ServiceName: "api",
			},
			prepareMock: func(*swarm.MockServiceManager) {},
			assertError: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "stack_name is required", "unexpected error")
			},
		},
		{
			name: "missing service name",
			request: getServiceHealthRequest{
				StackName: "core",
			},
			prepareMock: func(*swarm.MockServiceManager) {},
			assertError: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "service_name is required", "unexpected error")
			},
		},
		{
			name: "service inspect error",
			request: getServiceHealthRequest{
				StackName:   "core",
				ServiceName: "api",
			},
			prepareMock: func(inspector *swarm.MockServiceManager) {
				inspector.EXPECT().
					Get(gomock.Any(), swarm.NewServiceReference("core", "api")).
					Return(swarm.Service{}, assert.AnError)
			},
			assertError: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, assert.AnError, "unexpected inspect error")
			},
		},
		{
			name: "service tasks error",
			request: getServiceHealthRequest{
				StackName:   "core",
				ServiceName: "api",
			},
			prepareMock: func(inspector *swarm.MockServiceManager) {
				serviceRef := swarm.NewServiceReference("core", "api")
				inspector.EXPECT().
					Get(gomock.Any(), serviceRef).
					Return(swarm.Service{}, nil)
				inspector.EXPECT().
					ListTasks(gomock.Any(), serviceRef).
					Return(nil, assert.AnError)
			},
			assertError: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, assert.AnError, "unexpected tasks error")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			inspector := swarm.NewMockServiceManager(ctrl)
			testCase.prepareMock(inspector)

			tool := NewGetServiceHealth(inspector)
			_, err := tool.Execute(context.Background(), routing.Request{
				Payload: testCase.request,
			})
			require.Error(t, err, "expected execute error")
			testCase.assertError(t, err)
		})
	}
}
