package deployer

import (
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func newTestSecretResolver() *SecretResolver {
	return newTestSecretResolverWithIdentity(nil)
}

func newTestSecretResolverWithIdentity(identity []byte) *SecretResolver {
	return NewSecretResolver(NewAgeDecoder(identity), PlaintextDecoder{})
}

func TestSecretResolverValidatesReusedSecrets(t *testing.T) {
	for _, scenario := range []string{"corrupt file", "wrong identity", "missing identity"} {
		t.Run(scenario, func(t *testing.T) {
			identity, err := age.GenerateX25519Identity()
			require.NoError(t, err)
			ctrl := gomock.NewController(t)
			secrets := swarm.NewMockSecretManager(ctrl)
			secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"existing-password"}}).
				Return([]swarm.Secret{{ID: "existing-id", Name: "existing-password", Labels: map[string]string{"com.docker.stack.namespace": "app"}}}, nil)
			runner := NewMockRunner(ctrl)
			runner.EXPECT().Run(gomock.Any(), "-c", "rendered.yaml", "app").Return("", nil)
			d := &Deployer{resources: newResourceReconciler(&swarm.Swarm{Secrets: secrets}),
				secretResolver: newTestSecretResolverWithIdentity([]byte(identity.String())), runner: runner,
				serviceWaiter: newSuccessfulServiceWaiter(t)}
			desired := compose.Compose{Secrets: compose.Secrets{"password": {Name: "existing-password", File: "password.age", Data: encryptTestSecret(t, identity.Recipient(), []byte("private-marker"), false)}}}
			_, err = d.DeployStack(t.Context(), "app", "rendered.yaml", desired)
			require.NoError(t, err)
			switch scenario {
			case "corrupt file":
				desired.Secrets["password"].Data = []byte("private-corrupt-marker")
			case "wrong identity":
				other, keyErr := age.GenerateX25519Identity()
				require.NoError(t, keyErr)
				d.secretResolver = newTestSecretResolverWithIdentity([]byte(other.String()))
			case "missing identity":
				d.secretResolver = newTestSecretResolver()
			}
			_, err = d.DeployStack(t.Context(), "app", "rendered.yaml", desired)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-corrupt-marker")
			assert.NotContains(t, err.Error(), identity.String())
			// No additional API, Init Job or Docker CLI calls are expected.
		})
	}
}
