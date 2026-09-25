package stackloop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestRotatorRejectsSOPSSecretWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "api-key.sops")
	require.NoError(t, os.WriteFile(sourcePath, []byte("encrypted"), 0o600))

	file := &compose.File{
		Path: filepath.Join(dir, "compose.yaml"),
		Compose: compose.Compose{
			Secrets: compose.SharedObjects{
				"api-key": {
					Alias: "api-key",
					File:  "./api-key.sops",
				},
			},
		},
	}

	_, _, err := NewRotator().Rotate(file, "app", 8, false, config.SecretRotationSOPSSpec{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "uses .sops suffix but SOPS support is disabled")
}

func TestRotatorDecryptsSOPSSecretBeforeHashing(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "api-key.sops")
	require.NoError(t, os.WriteFile(sourcePath, []byte("encrypted"), 0o600))

	binDir := t.TempDir()
	sopsPath := filepath.Join(binDir, "sops")
	require.NoError(t, os.WriteFile(sopsPath, []byte("#!/bin/sh\nprintf decrypted-secret"), 0o700))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	keyPath := filepath.Join(dir, "age-key.txt")
	require.NoError(t, os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-TEST"), 0o600))

	file := &compose.File{
		Path: filepath.Join(dir, "compose.yaml"),
		Compose: compose.Compose{
			Secrets: compose.SharedObjects{
				"api-key": {
					Alias: "api-key",
					File:  "./api-key.sops",
				},
			},
		},
	}

	changed, temporaryFiles, err := NewRotator().Rotate(
		file,
		"app",
		8,
		false,
		config.SecretRotationSOPSSpec{
			Enabled: true,
			Age: config.SecretRotationSOPSAgeSpec{
				KeyFile: keyPath,
			},
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, temporaryFiles, 1)
	t.Cleanup(func() {
		cleanupMaterializedSecrets(temporaryFiles)
	})

	materialized, err := os.ReadFile(temporaryFiles[0])
	require.NoError(t, err)
	assert.Equal(t, []byte("decrypted-secret"), materialized)
	assert.Equal(t, temporaryFiles[0], file.Compose.Secrets["api-key"].File)

	expectedName := NewRotator().buildRotatedObjectName(
		"app",
		"api-key",
		"./api-key.sops",
		[]byte("decrypted-secret"),
		8,
		false,
	)
	assert.Equal(t, expectedName, file.Compose.Secrets["api-key"].Name)
}
