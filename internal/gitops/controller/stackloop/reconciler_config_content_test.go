package stackloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestReconcilerLoadRepositoryConfigContents(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pomerium.yaml"), []byte("pomerium"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agentgateway.yaml"), []byte("agentgateway"), 0o600))

	reconciler := &Reconciler{fileSystem: fs.NewLocalFileSystem()}
	desired := &compose.File{
		Path: filepath.Join(dir, "compose.yaml"),
		Compose: compose.Compose{
			Configs: compose.SharedObjects{
				"pomerium_config": {
					File: "./pomerium.yaml",
				},
				"agentgateway_config": {
					Name: "agentgateway-runtime-config",
					File: "./agentgateway.yaml",
				},
				"external_config": {
					External: true,
					File:     "./ignored.yaml",
				},
			},
		},
	}

	contents := reconciler.loadRepositoryConfigContents(context.Background(), "infra", desired)

	assert.Equal(t, map[string][]byte{
		"infra_pomerium_config":       []byte("pomerium"),
		"agentgateway-runtime-config": []byte("agentgateway"),
	}, contents)
}
