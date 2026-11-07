package deployer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	dockernetwork "github.com/docker/docker/api/types/network"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/networks"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHostNetworkExternalReplicasConvergesAndRepeatedDeployIsIdempotent(t *testing.T) {
	for _, single := range []bool{false, true} {
		name := "stack"
		if single {
			name = "service"
		}
		t.Run(name, func(t *testing.T) {
			// Engine exposes a local host network by default, but resolves service
			// attachments to the separate predefined network in the Swarm store.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1.48/networks/host", r.URL.Path)
				network := dockernetwork.Inspect{ID: "local-host-id", Name: "host", Scope: "local", Driver: "host"}
				if r.URL.Query().Get("scope") == "swarm" {
					network.ID, network.Scope = "swarm-host-id", "swarm"
				}
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(network))
			}))
			defer server.Close()
			docker, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithHTTPClient(server.Client()), client.WithVersion("1.48"))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, docker.Close()) })
			networkManager := swarm.NewSwarm(docker).Networks
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			var accepted dockerswarm.Service
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").DoAndReturn(func(_ any, _ string) (dockerswarm.Service, error) {
				if accepted.ID == "" {
					return dockerswarm.Service{}, swarm.ErrServiceNotFound
				}
				return accepted, nil
			}).AnyTimes()
			manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
					spec.TaskTemplate.Networks = slices.Clone(spec.TaskTemplate.Networks)
					require.Len(t, spec.TaskTemplate.Networks, 1)
					spec.TaskTemplate.Networks[0].Target = "swarm-host-id"
					accepted = dockerswarm.Service{ID: "job-id", Spec: spec, Meta: dockerswarm.Meta{CreatedAt: time.Now(), UpdatedAt: time.Now()}}
					return accepted.ID, nil
				})
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).DoAndReturn(func(_ any, _ swarm.ListServicesFilter) ([]swarm.StackService, error) {
				if accepted.ID == "" {
					return nil, nil
				}
				return []swarm.StackService{{ID: accepted.ID, Name: "job", Mode: "replicated", Replicas: accepted.Spec.Mode.Replicated.Replicas,
					ServiceSpec: accepted.Spec, CreatedAt: accepted.CreatedAt, UpdatedAt: accepted.UpdatedAt}}, nil
			}).AnyTimes()
			manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return(nil, nil).AnyTimes()
			d := &Deployer{secretResolver: newTestSecretResolver(), services: manager,
				converter: converter.New(networkManager, swarm.NewMockConfigManager(ctrl), swarm.NewMockSecretManager(ctrl)),
				networks:  networks.New(networkManager), serviceApplier: services.New(manager, &registry.NopAuthManager{}),
				serviceWaiter: waiterpkg.NewServiceWaiter(manager, swarm.NewMockNodeManager(ctrl), time.Millisecond, time.Second)}
			desired, err := compose.Parse([]byte(`
services:
  job:
    image: alpine:3
    networks: [host]
    deploy:
      replicas: 0
      labels:
        org.swarm-deploy.service.sync.policy.replicasManaged: "false"
networks:
  host:
    external: true
`))
			require.NoError(t, err)
			for attempt := range 3 {
				var result Result
				if single {
					result, err = d.DeployService(t.Context(), ServiceRequest{StackName: "demo", ServiceName: "job", Desired: *desired})
				} else {
					result, err = d.DeployStack(t.Context(), StackRequest{StackName: "demo", Desired: *desired})
				}
				require.NoError(t, err, "deploy attempt %d", attempt)
				assert.True(t, result.Verification.Known)
				assert.Empty(t, result.Verification.UnresolvedServices)
				assert.Zero(t, *accepted.Spec.Mode.Replicated.Replicas)
				if attempt == 0 {
					assert.Equal(t, []string{"job"}, result.SpecificationOnlyServices)
				}
			}
			// No Update expectation: any write after initial creation fails the test.
		})
	}
}
