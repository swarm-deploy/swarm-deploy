package waiter

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestServiceWaiterRetriesNormalizedDockerUnavailability(t *testing.T) {
	// Obtain the same structured error that a real Swarm manager supplies.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"Docker unavailable"}`))
	}))
	t.Cleanup(server.Close)
	dockerClient, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.48"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dockerClient.Close()) })
	_, unavailable := swarm.NewSwarm(dockerClient).Services.ListServices(t.Context(), swarm.ListServicesFilter{})
	var classified *swarm.DockerUnavailableError
	require.ErrorAs(t, unavailable, &classified)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 0, serviceWaitTestTime, "busybox:1.36")
	gomock.InOrder(
		manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, unavailable),
		manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil),
	)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, nil)
	_, err = NewServiceWaiter(manager, nil, time.Millisecond, time.Second).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
	})
	require.NoError(t, err)
	assert.True(t, isTransientDockerAPIError(unavailable))
}
