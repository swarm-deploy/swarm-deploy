package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestHandlerListSwarmServices(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}
	updatedAt := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	serviceInspector.EXPECT().
		ListServices(gomock.Any(), swarm.ListServicesFilter{}).
		Return([]swarm.StackService{
			{
				ID:           "service-1",
				Name:         "postgres",
				FullName:     "infra_postgres",
				Stack:        "infra",
				Image:        "postgres:18",
				Mode:         "replicated",
				RunningTasks: 1,
				DesiredTasks: 1,
				UpdatedAt:    updatedAt,
			},
			{
				ID:           "service-2",
				Name:         "standalone",
				FullName:     "standalone",
				Mode:         "global",
				RunningTasks: 2,
				DesiredTasks: 3,
				UpdatedAt:    updatedAt,
			},
		}, nil)

	response, err := h.ListSwarmServices(context.Background())
	require.NoError(t, err)
	require.Len(t, response.Services, 2)
	assert.Equal(t, "infra", response.Services[0].Stack)
	assert.Empty(t, response.Services[1].Stack)
	assert.Equal(t, int64(2), response.Services[1].Replicas.Running)
	assert.Equal(t, int64(3), response.Services[1].Replicas.Desired)
}

func TestSwarmServiceListResponseDoesNotContainEnvironment(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}
	serviceInspector.EXPECT().
		ListServices(gomock.Any(), swarm.ListServicesFilter{}).
		Return([]swarm.StackService{{ID: "service-1", Name: "api", FullName: "api"}}, nil)

	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/swarm/services", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.NotContains(t, recorder.Body.String(), "environment")
}

func TestHandlerListSwarmServicesDockerError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}
	serviceInspector.EXPECT().
		ListServices(gomock.Any(), swarm.ListServicesFilter{}).
		Return(nil, assert.AnError)

	_, err := h.ListSwarmServices(context.Background())
	require.Error(t, err)
	var statusErr *statusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusInternalServerError, statusErr.code)
}
