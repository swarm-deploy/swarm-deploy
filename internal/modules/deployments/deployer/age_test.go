package deployer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	secretpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/secrets"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func encryptTestSecret(t *testing.T, recipient age.Recipient, plaintext []byte, armored bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	var output io.Writer = &buf
	var encoded io.WriteCloser
	if armored {
		encoded = armor.NewWriter(output)
		output = encoded
	}
	w, err := age.Encrypt(output, recipient)
	require.NoError(t, err)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	if encoded != nil {
		require.NoError(t, encoded.Close())
	}
	return buf.Bytes()
}

func TestReconcileAgeSecrets(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(map[bool]string{false: "relative", true: "absolute"}[absolute], func(t *testing.T) {
			identity, err := age.GenerateX25519Identity()
			require.NoError(t, err)
			dir := t.TempDir()
			path := filepath.Join(dir, "password.age")
			ciphertext := encryptTestSecret(t, identity.Recipient(), []byte("private-marker"), false)
			require.NoError(t, os.WriteFile(path, ciphertext, 0o600))
			if !absolute {
				path = "./password.age"
			}
			ctrl := gomock.NewController(t)
			secrets := swarm.NewMockSecretManager(ctrl)
			secrets.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{Names: []string{"rotated-password"}}).Return(nil, nil)
			secrets.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req swarm.CreateSecretRequest) (string, error) {
				assert.Equal(t, []byte("private-marker"), req.Data)
				assert.Equal(t, "rotated-password", req.Name)
				assert.Equal(t, "app", req.Labels["com.docker.stack.namespace"])
				assert.Equal(t, "managed", req.Labels["purpose"])
				assert.Equal(t, "golang", req.TemplateDriver)
				return "secret-id", nil
			})
			r := newResourceReconciler(&swarm.Swarm{Secrets: secrets})
			objects := compose.Secrets{"password": {
				File: path, Name: "rotated-password", TemplateDriver: "golang",
				Labels: *compose.NewLabels(map[string]string{"purpose": "managed"}),
			}}
			data, err := newTestSecretResolverWithIdentity([]byte(identity.String())).Resolve(t.Context(), "app", filepath.Join(dir, "compose.yaml"), objects)
			require.NoError(t, err)
			defer secretpkg.Clear(data)
			resolved, err := r.Reconcile(t.Context(), "app", nil, objects, data)
			require.NoError(t, err)
			assert.Equal(t, ResolvedResource{ID: "secret-id", Name: "rotated-password"}, resolved.secrets["password"])
			stored, err := os.ReadFile(filepath.Join(dir, "password.age"))
			require.NoError(t, err)
			assert.Equal(t, ciphertext, stored)
		})
	}
}

func TestReconcileAgeSecretFailuresDoNotApplyServices(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	for _, tt := range []struct {
		name        string
		key         []byte
		contents    []byte
		driver      string
		createError bool
		want        string
	}{
		{name: "missing identity", contents: []byte("private-marker"), want: "requires secretRotation.age.identityFile"},
		{name: "bad ciphertext", key: []byte(identity.String()), contents: []byte("private-marker"), want: "invalid ciphertext"},
		{name: "driver", driver: "vault", want: "cannot combine a secret file with an external driver"},
		{name: "remote echo", key: []byte(identity.String()), contents: encryptTestSecret(t, identity.Recipient(), []byte("private-marker"), false), createError: true, want: "Docker API request failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "password.age"), tt.contents, 0o600))
			ctrl := gomock.NewController(t)
			secrets := swarm.NewMockSecretManager(ctrl)
			if tt.createError {
				secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil)
				secrets.EXPECT().Create(gomock.Any(), gomock.Any()).Return("", errors.New("private-marker "+identity.String()))
			}

			d := &Deployer{secretResolver: newTestSecretResolverWithIdentity(tt.key), resources: newResourceReconciler(&swarm.Swarm{Secrets: secrets}),
				serviceWaiter: newSuccessfulServiceWaiter(t)}
			serviceManager := swarm.NewMockServiceManager(ctrl)
			wireNativeTestDeployerWithServiceManager(t, d, serviceManager, false)
			_, err := d.DeployStack(t.Context(), "app", filepath.Join(dir, "compose.yaml"), "unused", compose.Compose{
				Services: minimalCompose("api").Services,
				Secrets:  compose.Secrets{"password": {File: "password.age", Driver: tt.driver}},
			})
			require.ErrorContains(t, err, tt.want)
			assert.NotContains(t, err.Error(), "private-marker")
			assert.NotContains(t, err.Error(), identity.String())
		})
	}
}

func TestReadPlaintextSecretIgnoresAgeIdentity(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password.txt"), []byte("plain"), 0o600))
	resolver := newTestSecretResolverWithIdentity([]byte("invalid identity"))
	objects := compose.Secrets{"password": {File: "password.txt"}}
	data, err := resolver.Resolve(t.Context(), "app", filepath.Join(dir, "compose.yaml"), objects)
	require.NoError(t, err)
	assert.Equal(t, "plain", string(data["password"]))
	objects["password"].File = ""
	_, err = resolver.Resolve(t.Context(), "app", filepath.Join(dir, "compose.yaml"), objects)
	require.ErrorContains(t, err, "file is required")
	objects["password"].File = "missing.age"
	_, err = resolver.Resolve(t.Context(), "app", filepath.Join(dir, "compose.yaml"), objects)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "invalid identity")
}
