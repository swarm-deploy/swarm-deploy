package deployer

import (
	"context"
	"strings"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/networks"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func wireNativeTestDeployer(t *testing.T, deployer *Deployer) *swarm.MockServiceManager {
	return wireNativeTestDeployerWithServiceManager(t, deployer, nil, true)
}

func wireNativeTestDeployerWithServiceManager(
	t *testing.T,
	deployer *Deployer,
	serviceManager *swarm.MockServiceManager,
	defaultApply bool,
) *swarm.MockServiceManager {
	t.Helper()
	ctrl := gomock.NewController(t)
	networkManager := swarm.NewMockNetworkManager(ctrl)
	networkManager.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, name string) (swarm.Network, error) {
			stack := strings.SplitN(name, "_", 2)[0]
			return swarm.Network{ID: "network-" + name, Name: name, Scope: "swarm", Driver: "overlay",
				Labels: map[string]string{labelsdict.StackNamespace: stack}}, nil
		}).AnyTimes()
	configManager := swarm.NewMockConfigManager(ctrl)
	configManager.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, filter swarm.ListConfigsFilter) ([]swarm.Config, error) {
			result := make([]swarm.Config, 0, len(filter.Names))
			for _, name := range filter.Names {
				result = append(result, swarm.Config{ID: "id-" + name, Name: name})
			}
			return result, nil
		}).AnyTimes()
	secretManager := swarm.NewMockSecretManager(ctrl)
	secretManager.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, filter swarm.ListSecretsFilter) ([]swarm.Secret, error) {
			result := make([]swarm.Secret, 0, len(filter.Names))
			for _, name := range filter.Names {
				result = append(result, swarm.Secret{ID: "id-" + name, Name: name})
			}
			return result, nil
		}).AnyTimes()
	if serviceManager == nil {
		serviceManager = swarm.NewMockServiceManager(ctrl)
	}
	if defaultApply {
		serviceManager.EXPECT().Inspect(gomock.Any(), gomock.Any()).Return(
			dockerswarm.Service{}, swarm.ErrServiceNotFound,
		).AnyTimes()
		serviceManager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("service-id", nil).AnyTimes()
	}

	deployer.converter = converter.New(networkManager, configManager, secretManager)
	deployer.networks = networks.New(networkManager)
	deployer.serviceApplier = services.New(serviceManager, &registry.NopAuthManager{})
	if deployer.serviceWaiter == nil {
		deployer.serviceWaiter = newSuccessfulServiceWaiter(t)
	}
	return serviceManager
}
