package deployer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDeployStackReconcilesResourcesWithAndWithoutInitJobs(t *testing.T) {
	for _, withInitJobs := range []bool{false, true} {
		for _, tt := range []struct {
			name       string
			existing   bool
			external   bool
			configName string
			secretName string
		}{
			{name: "create scoped resources"},
			{name: "reuse scoped resources", existing: true},
			{name: "create rotated resources", configName: "demo-config-abc123", secretName: "demo-secret-abc123"},
			{name: "reuse rotated resources", existing: true, configName: "demo-config-abc123", secretName: "demo-secret-abc123"},
			{name: "reuse external aliases", existing: true, external: true},
			{name: "reuse external names", existing: true, external: true, configName: "shared-config", secretName: "shared-secret"},
		} {
			t.Run(initJobTestName(withInitJobs)+"/"+tt.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				configs := swarm.NewMockConfigManager(ctrl)
				secrets := swarm.NewMockSecretManager(ctrl)
				jobs := NewMockInitJobExecutor(ctrl)

				dir := t.TempDir()
				config := &compose.Config{Alias: "app", Name: tt.configName, External: tt.external}
				secret := &compose.Secret{Alias: "password", Name: tt.secretName, External: tt.external}
				configName, secretName := tt.configName, tt.secretName
				if configName == "" {
					configName = "demo_app"
					if tt.external {
						configName = "app"
					}
				}
				if secretName == "" {
					secretName = "demo_password"
					if tt.external {
						secretName = "password"
					}
				}

				if !tt.external {
					secret.File = "password.txt"
					require.NoError(t, os.WriteFile(filepath.Join(dir, secret.File), []byte("secret-data"), 0o600))
				}
				var existingConfigs []swarm.Config
				var existingSecrets []swarm.Secret
				if tt.existing {
					existingConfigs = []swarm.Config{{ID: "config-id", Name: configName}}
					existingSecrets = []swarm.Secret{{ID: "secret-id", Name: secretName, Labels: map[string]string{"com.docker.stack.namespace": "demo"}}}
				} else {
					config.Data = []byte("config-data")
					config.Labels = *compose.NewLabels(map[string]string{"purpose": "app"})
					secret.File = "password.txt"
					secret.Labels = *compose.NewLabels(map[string]string{"purpose": "password"})
					require.NoError(t, os.WriteFile(filepath.Join(dir, secret.File), []byte("secret-data"), 0o600))
				}

				calls := []*gomock.Call{configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{Names: []string{configName}}).
					Return(existingConfigs, nil)}
				if !tt.existing {
					calls = append(calls, configs.EXPECT().Create(gomock.Any(), swarm.CreateConfigRequest{
						Name: configName, Data: config.Data,
						Labels: map[string]string{"purpose": "app", "com.docker.stack.namespace": "demo"},
					}).Return("config-id", nil))
				}
				calls = append(calls, secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{secretName}}).
					Return(existingSecrets, nil))
				if !tt.existing {
					calls = append(calls, secrets.EXPECT().Create(gomock.Any(), swarm.CreateSecretRequest{
						Name: secretName, Data: []byte("secret-data"),
						Labels: map[string]string{"purpose": "password", "com.docker.stack.namespace": "demo"},
					}).Return("secret-id", nil))
				}

				service := compose.Service{Name: "api", Image: "alpine:3"}
				if withInitJobs {
					service.InitJobs = []compose.InitJob{{Name: "migrate"}, {Name: "seed"}}
					for _, job := range service.InitJobs {
						calls = append(calls, jobs.EXPECT().Run(gomock.Any(), InitJobSpec{
							StackName: "demo", ServiceName: "api", Job: job,
							DefaultNetwork:  []string{"default"},
							NetworkTargets:  map[string]string{"default": "network-demo_default"},
							ResolvedConfigs: map[string]ResolvedResource{"app": {ID: "config-id", Name: configName}},
							ResolvedSecrets: map[string]ResolvedResource{"password": {ID: "secret-id", Name: secretName}},
						}).Return(InitJobResult{
							StartedAt: time.Now(), FinishedAt: time.Now(), Status: InitJobSucceeded,
						}, nil))
					}
				}
				for i := 1; i < len(calls); i++ {
					calls[i].After(calls[i-1])
				}

				deployer := &Deployer{secretResolver: newTestSecretResolver(),
					initJobRunner: jobs,
					resources:     &resourceReconciler{configs: configs, secrets: secrets},
					serviceWaiter: newSuccessfulServiceWaiter(t),
				}
				wireNativeTestDeployer(t, deployer)
				_, err := deployer.DeployStack(t.Context(), StackRequest{
					StackName:         "demo",
					SourceComposePath: filepath.Join(dir, "compose.yaml"),
					Desired: compose.Compose{
						Services: []compose.Service{service}, Configs: compose.Configs{"app": config}, Secrets: compose.Secrets{"password": secret},
					},
				})
				require.NoError(t, err)
			})
		}
	}
}

func TestDeployStackResourceFailuresStopDeployment(t *testing.T) {
	apiErr := errors.New("docker unavailable")
	for _, withInitJobs := range []bool{false, true} {
		for _, tt := range []struct {
			name       string
			stage      string
			config     bool
			external   bool
			errorMatch string
		}{
			{name: "list configs", stage: "list", config: true, errorMatch: "list configs"},
			{name: "create config", stage: "create", config: true, errorMatch: "create config demo_app"},
			{name: "missing external config", config: true, external: true, errorMatch: "external config app does not exist"},
			{name: "list secrets", stage: "list", errorMatch: "list secrets"},
			{name: "create secret", stage: "create", errorMatch: "create secret demo_password"},
			{name: "missing external secret", external: true, errorMatch: "external secret password does not exist"},
			{name: "read secret", stage: "read", errorMatch: "read secret demo_password"},
		} {
			t.Run(initJobTestName(withInitJobs)+"/"+tt.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				configs := swarm.NewMockConfigManager(ctrl)
				secrets := swarm.NewMockSecretManager(ctrl)
				jobs := NewMockInitJobExecutor(ctrl)

				dir := t.TempDir()
				desired := compose.Compose{Services: []compose.Service{{Name: "api", Image: "alpine:3"}}}
				if withInitJobs {
					desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate"}}
				}
				var listErr error
				if tt.stage == "list" {
					listErr = apiErr
				}
				if tt.config {
					desired.Configs = compose.Configs{"app": {External: tt.external, Data: []byte("data")}}
					configs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, listErr)
					if tt.stage == "create" {
						configs.EXPECT().Create(gomock.Any(), gomock.Any()).Return("", apiErr)
					}
				} else {
					desired.Secrets = compose.Secrets{"password": {External: tt.external, File: "password.txt"}}
					if tt.stage != "read" {
						secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, listErr)
					}
					if tt.stage == "list" {
						require.NoError(t, os.WriteFile(filepath.Join(dir, "password.txt"), []byte("data"), 0o600))
					}
					if tt.stage == "create" {
						// A later failure must also retain resources already created in this attempt.
						desired.Configs = compose.Configs{"app": {Data: []byte("data")}}
						gomock.InOrder(
							configs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil),
							configs.EXPECT().Create(gomock.Any(), gomock.Any()).Return("config-id", nil),
						)
						require.NoError(t, os.WriteFile(filepath.Join(dir, "password.txt"), []byte("data"), 0o600))
						secrets.EXPECT().Create(gomock.Any(), gomock.Any()).Return("", apiErr)
					}
				}
				deployer := &Deployer{secretResolver: newTestSecretResolver(),
					initJobRunner: jobs,
					resources:     &resourceReconciler{configs: configs, secrets: secrets},
					serviceWaiter: newSuccessfulServiceWaiter(t),
				}
				wireNativeTestDeployer(t, deployer)
				_, err := deployer.DeployStack(context.Background(), StackRequest{
					StackName:         "demo",
					SourceComposePath: filepath.Join(dir, "compose.yaml"),
					Desired:           desired,
				})
				require.ErrorContains(t, err, "")
				assert.ErrorContains(t, err, tt.errorMatch)
				if tt.stage == "list" || (tt.stage == "create" && tt.config) {
					assert.ErrorIs(t, err, apiErr)
				}
				// Unexpected job, deploy, or cleanup calls fail through gomock.
			})
		}
	}
}

func TestDeployStackDeployFailureKeepsReconciledResources(t *testing.T) {
	for _, withInitJobs := range []bool{false, true} {
		t.Run(initJobTestName(withInitJobs), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			configs := swarm.NewMockConfigManager(ctrl)
			secrets := swarm.NewMockSecretManager(ctrl)
			jobs := NewMockInitJobExecutor(ctrl)

			deployErr := errors.New("deploy failed")
			desired := compose.Compose{
				Configs:  compose.Configs{"app": {Data: []byte("data")}},
				Services: []compose.Service{{Name: "api", Image: "alpine:3"}},
			}
			gomock.InOrder(
				configs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil),
				configs.EXPECT().Create(gomock.Any(), gomock.Any()).Return("config-id", nil),
			)
			if withInitJobs {
				desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate"}}
				jobs.EXPECT().Run(gomock.Any(), gomock.Any()).Return(InitJobResult{
					StartedAt: time.Now(), FinishedAt: time.Now(), Status: InitJobSucceeded,
				}, nil)
			}
			deployer := &Deployer{secretResolver: newTestSecretResolver(),
				initJobRunner: jobs,
				resources:     &resourceReconciler{configs: configs, secrets: secrets},
				serviceWaiter: newSuccessfulServiceWaiter(t),
			}
			serviceManager := swarm.NewMockServiceManager(ctrl)
			serviceManager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
			serviceManager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("", deployErr)
			wireNativeTestDeployerWithServiceManager(t, deployer, serviceManager, false)
			result, err := deployer.DeployStack(t.Context(), StackRequest{
				StackName:         "demo",
				SourceComposePath: "compose.yaml",
				Desired:           desired,
			})
			require.ErrorIs(t, err, deployErr)
			assert.ErrorContains(t, err, "create service api")
			if withInitJobs {
				require.Len(t, result.InitJobs, 1)
			} else {
				assert.Empty(t, result.InitJobs)
			}
			// No Remove calls are expected, matching existing failure/cleanup behavior.
		})
	}
}

func initJobTestName(withInitJobs bool) string {
	if withInitJobs {
		return "with init jobs"
	}
	return "without init jobs"
}
