package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestListServiceTasksReturnsAllStatesNewestFirstWithPagination(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	tasks := []swarm.ServiceTask{
		{ID: "failed", Node: "node-1", CreatedAt: baseTime.Add(time.Minute), UpdatedAt: baseTime.Add(2 * time.Minute), CurrentState: swarm.TaskStateFailed, Error: "exit code 1"},
		{ID: "running", Node: "node-2", CreatedAt: baseTime.Add(4 * time.Minute), UpdatedAt: baseTime.Add(5 * time.Minute), CurrentState: swarm.TaskStateRunning},
		{ID: "shutdown", Node: "node-3", CreatedAt: baseTime, UpdatedAt: baseTime.Add(time.Minute), CurrentState: swarm.TaskStateShutdown},
		{ID: "rejected", Node: "node-4", CreatedAt: baseTime.Add(3 * time.Minute), UpdatedAt: baseTime.Add(4 * time.Minute), CurrentState: swarm.TaskStateRejected, Error: "no suitable node"},
		{ID: "complete", Node: "node-5", CreatedAt: baseTime.Add(2 * time.Minute), UpdatedAt: baseTime.Add(3 * time.Minute), CurrentState: swarm.TaskStateComplete},
	}

	testCases := []struct {
		name           string
		limit          int
		offset         int
		expectedIDs    []string
		expectedStates []swarm.TaskState
		hasMore        bool
		nextOffset     *int
	}{
		{
			name:           "first page",
			limit:          2,
			expectedIDs:    []string{"running", "rejected"},
			expectedStates: []swarm.TaskState{swarm.TaskStateRunning, swarm.TaskStateRejected},
			hasMore:        true,
			nextOffset:     intPointer(2),
		},
		{
			name:           "older page includes terminal tasks",
			limit:          3,
			offset:         2,
			expectedIDs:    []string{"complete", "failed", "shutdown"},
			expectedStates: []swarm.TaskState{swarm.TaskStateComplete, swarm.TaskStateFailed, swarm.TaskStateShutdown},
		},
		{
			name:           "offset beyond available tasks",
			limit:          2,
			offset:         10,
			expectedIDs:    []string{},
			expectedStates: []swarm.TaskState{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			services := swarm.NewMockServiceManager(ctrl)
			services.EXPECT().
				ListTasks(gomock.Any(), swarm.NewServiceReference("payments", "api")).
				Return(tasks, nil)
			tool := NewListServiceTasks(services)

			response, err := tool.Execute(context.Background(), routing.Request{Payload: listServiceTasksRequest{
				StackName:   " payments ",
				ServiceName: " api ",
				Limit:       intPointer(testCase.limit),
				Offset:      testCase.offset,
			}})
			require.NoError(t, err)

			var payload struct {
				StackName   string                `json:"stack_name"`
				ServiceName string                `json:"service_name"`
				Tasks       []serviceTaskResponse `json:"tasks"`
				Count       int                   `json:"count"`
				Offset      int                   `json:"offset"`
				HasMore     bool                  `json:"has_more"`
				NextOffset  *int                  `json:"next_offset"`
			}
			encoded, err := json.Marshal(response.Payload)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &payload))

			assert.Equal(t, "payments", payload.StackName)
			assert.Equal(t, "api", payload.ServiceName)
			assert.Equal(t, testCase.expectedIDs, taskIDs(payload.Tasks))
			assert.Equal(t, testCase.expectedStates, taskStates(payload.Tasks))
			assert.Equal(t, len(testCase.expectedIDs), payload.Count)
			assert.Equal(t, testCase.offset, payload.Offset)
			assert.Equal(t, testCase.hasMore, payload.HasMore)
			assert.Equal(t, testCase.nextOffset, payload.NextOffset)
		})
	}
}

func TestListServiceTasksIncludesTaskDetails(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	ctrl := gomock.NewController(t)
	services := swarm.NewMockServiceManager(ctrl)
	services.EXPECT().
		ListTasks(gomock.Any(), swarm.NewServiceReference("payments", "api")).
		Return([]swarm.ServiceTask{{
			ID:           "task-1",
			Node:         "node-1",
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
			CurrentState: swarm.TaskStateFailed,
			Error:        "exit code 1",
		}}, nil)
	tool := NewListServiceTasks(services)

	response, err := tool.Execute(context.Background(), routing.Request{Payload: listServiceTasksRequest{
		StackName:   "payments",
		ServiceName: "api",
	}})
	require.NoError(t, err)

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err)
	var payload struct {
		Tasks []serviceTaskResponse `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Len(t, payload.Tasks, 1)
	assert.Equal(t, serviceTaskResponse{
		ID:        "task-1",
		Node:      "node-1",
		State:     swarm.TaskStateFailed,
		Error:     "exit code 1",
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, payload.Tasks[0])
	assert.Equal(t, config.AssistantToolNameServiceTaskList, tool.Definition().Name)
}

func TestListServiceTasksRejectsInvalidPagination(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		request listServiceTasksRequest
		message string
	}{
		{name: "missing stack", request: listServiceTasksRequest{ServiceName: "api"}, message: "stack_name is required"},
		{name: "missing service", request: listServiceTasksRequest{StackName: "payments"}, message: "service_name is required"},
		{name: "zero limit", request: listServiceTasksRequest{StackName: "payments", ServiceName: "api", Limit: intPointer(0)}, message: "limit must be > 0"},
		{name: "excessive limit", request: listServiceTasksRequest{StackName: "payments", ServiceName: "api", Limit: intPointer(maxServiceTaskListLimit + 1)}, message: "limit must be <="},
		{name: "negative offset", request: listServiceTasksRequest{StackName: "payments", ServiceName: "api", Offset: -1}, message: "offset must be >= 0"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tool := NewListServiceTasks(swarm.NewMockServiceManager(ctrl))
			_, err := tool.Execute(context.Background(), routing.Request{Payload: testCase.request})
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.message)
		})
	}
}

func taskIDs(tasks []serviceTaskResponse) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func taskStates(tasks []serviceTaskResponse) []swarm.TaskState {
	states := make([]swarm.TaskState, 0, len(tasks))
	for _, task := range tasks {
		states = append(states, task.State)
	}
	return states
}
