package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestGetStackResourcesMetadata(t *testing.T) {
	t.Parallel()
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned metadata", true: "empty arrays"}[empty], func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			networks := swarm.NewMockNetworkManager(ctrl)
			secrets := swarm.NewMockSecretManager(ctrl)
			configs := swarm.NewMockConfigManager(ctrl)
			owned := map[string]string{labelsdict.StackNamespace: "app"}
			foreign := map[string]string{labelsdict.StackNamespace: "other"}
			netItems := []swarm.Network{{ID: "foreign", Name: "other", Labels: foreign}, {ID: "prefix", Name: "app_external"}}
			secretItems := []swarm.Secret{{ID: "foreign", Name: "other", Labels: foreign}, {ID: "prefix", Name: "app_external"}}
			configItems := []swarm.Config{{ID: "foreign", Name: "other", Labels: foreign}, {ID: "prefix", Name: "app_external"}}
			if !empty {
				netItems = append(netItems, swarm.Network{ID: "net", Name: "app_default", Driver: "overlay", Scope: "swarm", Labels: owned})
				secretItems = append(secretItems, swarm.Secret{ID: "sec", Name: "app_password", Labels: owned})
				configItems = append(configItems, swarm.Config{ID: "cfg-z", Name: "z-config", Labels: owned, Data: []byte("DO_NOT_LEAK")}, swarm.Config{ID: "cfg-a", Name: "a-config", Labels: owned})
			}
			networks.EXPECT().List(gomock.Any()).Return(netItems, nil)
			secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{StackName: "app"}).Return(secretItems, nil)
			configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{StackName: "app"}).Return(configItems, nil)
			h := &handler{
				stackProvider: newConfigWithStacks(t, []config.StackSpec{{Name: "app", ComposeFile: "app.yaml"}}),
				networks:      networks, dockerSecrets: secrets, dockerConfigs: configs,
			}
			server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/stacks/app/resources", nil))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var result generated.StackResourcesResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			if empty {
				assert.JSONEq(t, `{"networks":[],"secrets":[],"configs":[]}`, recorder.Body.String())
			} else {
				require.Len(t, result.Networks, 1)
				assert.Equal(t, "app", result.Networks[0].StackName.Value)
				assert.Equal(t, []generated.StackResourceInfo{{ID: "sec", Name: "app_password"}}, result.Secrets)
				assert.Equal(t, []generated.StackResourceInfo{{ID: "cfg-a", Name: "a-config"}, {ID: "cfg-z", Name: "z-config"}}, result.Configs)
			}
			assert.NotContains(t, recorder.Body.String(), "DO_NOT_LEAK")
			assert.NotContains(t, recorder.Body.String(), `"data"`)
			assert.NotContains(t, recorder.Body.String(), "app_external")
		})
	}
}

func TestGetStackResourcesErrors(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"unknown stack", "networks", "secrets", "configs"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			networks := swarm.NewMockNetworkManager(ctrl)
			secrets := swarm.NewMockSecretManager(ctrl)
			configs := swarm.NewMockConfigManager(ctrl)
			unavailable := errors.New("docker unavailable")
			h := &handler{
				stackProvider: newConfigWithStacks(t, []config.StackSpec{{Name: "app", ComposeFile: "app.yaml"}}),
				networks:      networks, dockerSecrets: secrets, dockerConfigs: configs,
			}
			name := "app"
			if stage == "unknown stack" {
				name = "unknown"
			} else if stage == "networks" {
				networks.EXPECT().List(gomock.Any()).Return(nil, unavailable)
			} else {
				networks.EXPECT().List(gomock.Any()).Return(nil, nil)
				if stage == "secrets" {
					secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{StackName: name}).Return(nil, unavailable)
				} else {
					secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{StackName: name}).Return(nil, nil)
					configs.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{StackName: name}).Return(nil, unavailable)
				}
			}
			result, err := h.GetStackResources(context.Background(), generated.GetStackResourcesParams{Stack: name})
			require.Error(t, err)
			assert.Nil(t, result, "an API failure must not become partial or zero resource counts")
			if stage == "unknown stack" {
				var status *statusError
				require.ErrorAs(t, err, &status)
				assert.Equal(t, http.StatusNotFound, status.code)
			} else {
				assert.ErrorIs(t, err, unavailable)
			}
		})
	}
}
