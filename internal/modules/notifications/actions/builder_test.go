package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder(t *testing.T) {
	tests := []struct {
		name             string
		origin, basePath string
		language         string
		deploymentID     string
		alertID          string
		wantDeployment   []Action
		wantAlert        []Action
	}{
		{name: "disabled", basePath: "/", language: "en", deploymentID: "deployment-1", alertID: "alert-1"},
		{
			name: "English root", origin: "https://swarm.example", basePath: "/", language: "en",
			deploymentID: "deployment/1", alertID: "alert/1",
			wantDeployment: []Action{{
				Text: "Open deployment", URL: "https://swarm.example/deployments?deployment_id=deployment%2F1",
			}},
			wantAlert: []Action{{
				Text: "Open alert", URL: "https://swarm.example/events?alert_id=alert%2F1&tab=alerts",
			}},
		},
		{
			name: "Russian base path", origin: "https://swarm.example:8443", basePath: "/infrastructure/swarm", language: "ru",
			deploymentID: "deployment-1", alertID: "alert-1",
			wantDeployment: []Action{{
				Text: "Деплоймент", URL: "https://swarm.example:8443/infrastructure/swarm/deployments?deployment_id=deployment-1",
			}},
			wantAlert: []Action{{
				Text: "Алерт", URL: "https://swarm.example:8443/infrastructure/swarm/events?alert_id=alert-1&tab=alerts",
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewBuilder(test.origin, test.basePath, test.language)
			require.NoError(t, err)
			assert.Equal(t, test.wantDeployment, builder.Deployment(test.deploymentID))
			assert.Equal(t, test.wantAlert, builder.Alert(test.alertID))
		})
	}
}

func TestBuilderOmitsActionsWithoutIdentifiers(t *testing.T) {
	builder, err := NewBuilder("https://swarm.example", "/", "en")
	require.NoError(t, err)
	assert.Nil(t, builder.Deployment(""))
	assert.Nil(t, builder.Alert(""))
}
