package deployer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestInvalidDesiredStateCannotMutateDockerResources(t *testing.T) {
	for _, option := range []string{
		"deploy: {mode: unknown}",
		"stop_grace_period: invalid",
		"ports: [{target: 80, mode: wrong}]",
		"healthcheck: {interval: invalid}",
		"networks: [missing]",
	} {
		t.Run(option, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			sw := &swarm.Swarm{
				Networks: swarm.NewMockNetworkManager(ctrl), Services: swarm.NewMockServiceManager(ctrl),
				Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
			}
			// All manager mocks are strict: even resource reads must wait for validation.
			d := NewDeployer(time.Millisecond, time.Second, nil, sw, nil, newTestSecretResolver())
			desired, err := compose.Parse([]byte("services: {app: {image: alpine, " + option + "}}\nconfigs: {app: {file: cfg}}\nsecrets: {password: {file: password}}"))
			require.NoError(t, err)
			for _, service := range []bool{false, true} {
				var result Result
				if service {
					result, err = d.DeployService(t.Context(), ServiceRequest{
						StackName:         "demo",
						SourceComposePath: "compose.yaml",
						ServiceName:       "app",
						Desired:           *desired,
					})
				} else {
					result, err = d.DeployStack(t.Context(), StackRequest{
						StackName:         "demo",
						SourceComposePath: "compose.yaml",
						Desired:           *desired,
					})
				}
				require.Error(t, err)
				assert.Equal(t, stepValidateDesired, FailedStep(err))
				if !service {
					assert.True(t, result.Verification.Known)
					assert.Empty(t, result.Verification.UnresolvedServices)
				}
			}
		})
	}
}
