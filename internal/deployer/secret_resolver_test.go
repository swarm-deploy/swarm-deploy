package deployer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
)

func newTestSecretResolver() *SecretResolver {
	return newTestSecretResolverWithIdentity(nil)
}

func newTestSecretResolverWithIdentity(identity []byte) *SecretResolver {
	return NewSecretResolver(fs.NewLocalFileSystem(), NewAgeDecoder(identity), PlaintextDecoder{})
}

func TestSecretResolverUsesInjectedTracingFileSystem(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	dir := t.TempDir()
	value := []byte("resolver-private-marker")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password.age"), encryptTestSecret(t, identity.Recipient(), value, false), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.txt"), value, 0o600))
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx, parent := provider.Tracer("test").Start(t.Context(), "resolve")
	defer parent.End()
	resolver := NewSecretResolver(fs.NewTraceableFileSystem(provider, fs.NewLocalFileSystem()),
		NewAgeDecoder([]byte(identity.String())), PlaintextDecoder{})
	data, err := resolver.Resolve(ctx, "app", filepath.Join(dir, "compose.yaml"), compose.Secrets{
		"encrypted": {File: "./password.age"},
		"plain":     {File: filepath.Join(dir, "plain.txt")},
		"external":  {External: true, File: "missing.age"},
		"driver":    {Driver: "vault"},
	})
	require.NoError(t, err)
	defer clearSecretData(data)
	assert.Equal(t, map[string][]byte{"encrypted": value, "plain": value}, data)
	spans := recorder.Ended()
	require.Len(t, spans, 2, "both decoders must use the injected traced filesystem")
	paths := make([]string, 0, len(spans))
	for _, span := range spans {
		assert.Equal(t, "fs.ReadFile", span.Name())
		assert.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID())
		assert.Equal(t, codes.Ok, span.Status().Code)
		for _, attr := range span.Attributes() {
			if attr.Key == "file.path" {
				paths = append(paths, attr.Value.AsString())
			}
		}
		diagnostic := fmt.Sprint(span.Attributes(), span.Events(), span.Status())
		assert.NotContains(t, diagnostic, string(value))
		assert.NotContains(t, diagnostic, identity.String())
	}
	assert.ElementsMatch(t, []string{filepath.Join(dir, "password.age"), filepath.Join(dir, "plain.txt")}, paths)
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
			runner.EXPECT().Run(gomock.Any(), "-c", "rendered.yaml", "app").Return("", nil)
			d := &Deployer{resources: newResourceReconciler(&swarm.Swarm{Secrets: secrets}),
				secretResolver: newTestSecretResolverWithIdentity([]byte(identity.String())), runner: runner}
			desired := compose.Compose{Secrets: compose.Secrets{"password": {Name: "existing-password", File: "password.age"}}}
			require.NoError(t, d.DeployStack(t.Context(), "app", filepath.Join(dir, "compose.yaml"), "rendered.yaml", desired, DeployOptions{}))
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
			err = d.DeployStack(t.Context(), "app", filepath.Join(dir, "compose.yaml"), "rendered.yaml", desired, DeployOptions{})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-corrupt-marker")
			assert.NotContains(t, err.Error(), identity.String())
			// No additional API, Init Job or Docker CLI calls are expected.
		})
	}
}

func TestSecretResolverDecoderSelection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("ordinary"), 0o600))
	resolver := NewSecretResolver(fs.NewLocalFileSystem(), NewAgeDecoder(nil))
	_, err := resolver.Resolve(t.Context(), "app", filepath.Join(dir, "compose.yaml"), compose.Secrets{
		"plain": {File: "plain.txt"},
	})
	require.ErrorContains(t, err, "no decoder for secret file")
}
