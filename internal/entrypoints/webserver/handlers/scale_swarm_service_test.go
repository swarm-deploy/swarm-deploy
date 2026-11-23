package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestScaleSwarmService(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		replicas   uint64
		stack      string
		eventError error
	}{
		{name: "scale up", replicas: 5, stack: "infra"},
		{name: "scale down", replicas: 1, stack: "infra"},
		{name: "scale to zero", replicas: 0, stack: "infra"},
		{name: "no-op", replicas: 3, stack: "infra"},
		{name: "standalone", replicas: 4},
		{name: "event failure after accepted update", replicas: 5, stack: "infra", eventError: errors.New("outbox unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			publisher := dispatcher.NewMockDispatcher(ctrl)
			manager.EXPECT().ScaleByID(gomock.Any(), "service-id", tc.replicas).
				Return(swarm.ServiceScaleResult{Stack: tc.stack, Service: "api", PreviousReplicas: 3}, nil)
			if tc.replicas > 3 {
				publisher.EXPECT().Publish(gomock.Any(), &events.ServiceReplicasIncreased{
					StackName: tc.stack, ServiceName: "api", PreviousReplicas: 3, CurrentReplicas: tc.replicas,
				}).Return(tc.eventError)
			} else if tc.replicas < 3 {
				publisher.EXPECT().Publish(gomock.Any(), &events.ServiceReplicasDecreased{
					StackName: tc.stack, ServiceName: "api", PreviousReplicas: 3, CurrentReplicas: tc.replicas,
				}).Return(tc.eventError)
			}
			h := &handler{serviceInspector: manager, eventDispatcher: publisher}
			err := h.ScaleSwarmService(t.Context(), &generated.ScaleSwarmServiceRequest{Replicas: int64(tc.replicas)},
				generated.ScaleSwarmServiceParams{ServiceID: "service-id"})
			require.NoError(t, err)
		})
	}
}

func TestScaleSwarmServiceHTTPErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		body        string
		dockerError error
		status      int
		message     string
	}{
		{name: "negative", body: `{"replicas":-1}`, status: 400},
		{name: "fraction", body: `{"replicas":1.5}`, status: 400},
		{name: "string", body: `{"replicas":"2"}`, status: 400},
		{name: "null", body: `{"replicas":null}`, status: 400},
		{name: "missing", body: `{}`, status: 400},
		{name: "overflow", body: `{"replicas":9223372036854775808}`, status: 400},
		{name: "invalid json", body: `{`, status: 400},
		{name: "unsupported mode", body: `{"replicas":2}`, dockerError: swarm.ErrServiceModeUnsupported, status: 400, message: "only replicated"},
		{name: "not found", body: `{"replicas":2}`, dockerError: swarm.ErrServiceNotFound, status: 404, message: "service not found"},
		{name: "conflict", body: `{"replicas":2}`, dockerError: swarm.ErrServiceUpdateConflict, status: 409, message: "refresh and retry"},
		{name: "docker error", body: `{"replicas":2}`, dockerError: errors.New("Docker unavailable"), status: 500, message: "unable to scale service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			publisher := dispatcher.NewMockDispatcher(ctrl)
			if tc.dockerError != nil {
				manager.EXPECT().ScaleByID(gomock.Any(), "service-id", uint64(2)).Return(swarm.ServiceScaleResult{}, tc.dockerError)
			}
			h := &handler{serviceInspector: manager, eventDispatcher: publisher}
			server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/swarm/services/service-id/replicas", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			assert.Contains(t, recorder.Body.String(), "error_message")
			assert.Contains(t, recorder.Body.String(), tc.message)
		})
	}
}

func TestScaleSwarmServiceHTTPAccepted(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	manager.EXPECT().ScaleByID(gomock.Any(), "service-id", uint64(0)).Return(swarm.ServiceScaleResult{}, nil)
	h := &handler{serviceInspector: manager, eventDispatcher: publisher}
	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/swarm/services/service-id/replicas", strings.NewReader(`{"replicas":0}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Empty(t, recorder.Body.String())
}
