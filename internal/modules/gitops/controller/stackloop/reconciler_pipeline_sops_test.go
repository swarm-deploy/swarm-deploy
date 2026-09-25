package stackloop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestValidateSecretsRejectsSOPSWhenRotationDisabled(t *testing.T) {
	reconciler := &Reconciler{
		cfg: &config.Config{},
	}
	payload := &pipelinePayload{
		Desired: &compose.File{
			Compose: compose.Compose{
				Secrets: compose.SharedObjects{
					"api-key": {
						File: "./secrets/api-key.sops",
					},
				},
			},
		},
	}

	err := reconciler.validateSecrets(context.Background(), payload)

	require.ErrorContains(t, err, "secret rotation is disabled")
}

func TestValidateSecretsRejectsSOPSWhenSOPSDisabled(t *testing.T) {
	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				SecretRotation: config.SecretRotationSpec{
					Enabled: true,
				},
			},
		},
	}
	payload := &pipelinePayload{
		Desired: &compose.File{
			Compose: compose.Compose{
				Secrets: compose.SharedObjects{
					"api-key": {
						File: "./secrets/api-key.sops",
					},
				},
			},
		},
	}

	err := reconciler.validateSecrets(context.Background(), payload)

	require.ErrorContains(t, err, "SOPS support is disabled")
}

func TestValidateSecretsAllowsSOPSWhenEnabled(t *testing.T) {
	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				SecretRotation: config.SecretRotationSpec{
					Enabled: true,
					SOPS: config.SecretRotationSOPSSpec{
						Enabled: true,
					},
				},
			},
		},
	}
	payload := &pipelinePayload{
		Desired: &compose.File{
			Compose: compose.Compose{
				Secrets: compose.SharedObjects{
					"api-key": {
						File: "./secrets/api-key.sops",
					},
				},
			},
		},
	}

	require.NoError(t, reconciler.validateSecrets(context.Background(), payload))
}
