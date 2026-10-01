package stackloop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestRotatorLabelsManagedResources(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "token"), []byte("secret"), 0o600), "write secret")
	file := &compose.File{
		Path: filepath.Join(dir, "compose.yaml"),
		Compose: compose.Compose{Secrets: compose.SharedObjects{
			"token":    {File: "./token"},
			"external": {External: true, Name: "external-token"},
		}},
	}

	changed, err := NewRotator().Rotate(file, "app", 8, false)

	require.NoError(t, err, "rotate secrets")
	assert.True(t, changed, "expected rotation mutation")
	require.NotNil(t, file.Compose.Secrets["token"].Labels, "expected rotation labels")
	assert.Equal(t, labelsdict.RotatedResourceManagedLabelValue,
		file.Compose.Secrets["token"].Labels.Map[labelsdict.RotatedResourceManagedLabelKey],
		"expected managed label")
	assert.Equal(t, "token",
		file.Compose.Secrets["token"].Labels.Map[labelsdict.RotatedResourceLogicalNameLabelKey],
		"expected logical name label")
	assert.Nil(t, file.Compose.Secrets["external"].Labels, "external secret must not be managed")
}
