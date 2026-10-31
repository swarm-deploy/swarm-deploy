package secrets

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
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSecretResolverUsesInjectedTracingFileSystem(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	dir := t.TempDir()
	value := []byte("resolver-private-marker")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password.age"),
		encryptTestSecret(t, identity.Recipient(), value, false), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.txt"), value, 0o600))
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder), sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
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
	defer Clear(data)
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
	assert.ElementsMatch(t,
		[]string{filepath.Join(dir, "password.age"), filepath.Join(dir, "plain.txt")}, paths)
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
