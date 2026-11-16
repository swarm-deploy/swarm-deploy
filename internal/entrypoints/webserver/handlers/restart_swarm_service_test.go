package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRestartSwarmServiceHTTP(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		restartError error
		inspectError error
		eventError   error
		status       int
		message      string
		standalone   bool
	}{
		{name: "success", status: 204},
		{name: "standalone", status: 204, standalone: true},
		{name: "publication failure", status: 204, eventError: errors.New("outbox unavailable")},
		{name: "global or job", restartError: swarm.ErrServiceModeUnsupported, status: 400, message: "only replicated services can be restarted"},
		{name: "missing", inspectError: fmt.Errorf("%w: private Docker diagnostic", swarm.ErrServiceNotFound), status: 404, message: "service not found"},
		{name: "zero replicas", restartError: swarm.ErrServiceZeroReplicas, status: 409, message: "cannot restart a service with zero configured replicas"},
		{name: "version conflict", restartError: fmt.Errorf("%w: private Docker diagnostic", swarm.ErrServiceUpdateConflict), status: 409, message: "service changed while restarting; refresh and retry"},
		{name: "docker failure", restartError: errors.New("private Docker diagnostic"), status: 500, message: "unable to restart service"},
		{name: "restoration failure", restartError: fmt.Errorf("restore service replicas to 3: %w", swarm.ErrServiceUpdateConflict), status: 409, message: "service changed while restarting; refresh and retry"},
		{name: "inspect failure", inspectError: errors.New("private Docker diagnostic"), status: 500, message: "unable to restart service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			publisher := dispatcher.NewMockDispatcher(ctrl)
			stack, fullName := "infra", "infra_api"
			if tc.standalone {
				stack, fullName = "", "api"
			}
			manager.EXPECT().Inspect(gomock.Any(), "service-id").Return(dockerswarm.Service{ID: "service-id", Spec: dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{Name: fullName, Labels: map[string]string{"com.docker.stack.namespace": stack}}}}, tc.inspectError)
			if tc.inspectError == nil {
				manager.EXPECT().Restart(gomock.Any(), swarm.NewServiceIDReference("service-id")).Return(uint64(3), tc.restartError)
				if tc.restartError == nil {
					publisher.EXPECT().Publish(gomock.Any(), &events.ServiceRestarted{StackName: stack, ServiceName: "api"}).Return(tc.eventError)
				}
			}
			h := &handler{serviceInspector: manager, eventDispatcher: publisher}
			server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/swarm/services/service-id/restart", nil))
			assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.status == 204 {
				assert.Empty(t, recorder.Body.String())
			} else {
				assert.JSONEq(t, fmt.Sprintf(`{"error_message":%q}`, tc.message), recorder.Body.String())
			}
		})
	}
}
