package deployer

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func newTestSecretResolver() *SecretResolver {
	return newTestSecretResolverWithIdentity(nil)
}

func newTestSecretResolverWithIdentity(identity []byte) *SecretResolver {
	return NewSecretResolver(fs.NewLocalFileSystem(), NewAgeDecoder(identity), PlaintextDecoder{})
}

func TestSecretResolverValidatesReusedSecrets(t *testing.T) {
	for _, scenario := range []string{"corrupt file", "wrong identity", "missing identity"} {
		t.Run(scenario, func(t *testing.T) {
			identity, err := age.GenerateX25519Identity()
			require.NoError(t, err)
			dir := t.TempDir()
			path := filepath.Join(dir, "password.age")
			require.NoError(t, os.WriteFile(path, encryptTestSecret(t, identity.Recipient(), []byte("private-marker"), false), 0o600))
			ctrl := gomock.NewController(t)
			secrets := swarm.NewMockSecretManager(ctrl)
			secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"existing-password"}}).
				Return([]swarm.Secret{{ID: "existing-id", Name: "existing-password", Labels: map[string]string{"com.docker.stack.namespace": "app"}}}, nil)
			runner := NewMockRunner(ctrl)
			d := &Deployer{resources: newResourceReconciler(&swarm.Swarm{Secrets: secrets}),
				secretResolver: newTestSecretResolverWithIdentity([]byte(identity.String())), runner: runner,
				serviceWaiter: newSuccessfulServiceWaiter(t)}
			wireNativeTestDeployer(t, d)
			desired := compose.Compose{Secrets: compose.Secrets{"password": {Name: "existing-password", File: "password.age"}}}
			_, err = d.DeployStack(t.Context(), "app", filepath.Join(dir, "compose.yaml"), "rendered.yaml", desired)
			require.NoError(t, err)
			switch scenario {
			case "corrupt file":
				require.NoError(t, os.WriteFile(path, []byte("private-corrupt-marker"), 0o600))
			case "wrong identity":
				other, keyErr := age.GenerateX25519Identity()
				require.NoError(t, keyErr)
				d.secretResolver = newTestSecretResolverWithIdentity([]byte(other.String()))
			case "missing identity":
				d.secretResolver = newTestSecretResolver()
			}
			_, err = d.DeployStack(t.Context(), "app", filepath.Join(dir, "compose.yaml"), "rendered.yaml", desired)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-corrupt-marker")
			assert.NotContains(t, err.Error(), identity.String())
			// No additional API, Init Job or Docker CLI calls are expected.
		})
	}
}
