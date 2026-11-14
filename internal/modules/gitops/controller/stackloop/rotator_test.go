package stackloop

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestRotatorLabelsManagedResources(t *testing.T) {
	file := &compose.File{
		// The path does not exist: rotation must rely only on content captured at load time.
		Path: filepath.Join(t.TempDir(), "missing", "compose.yaml"),
		Compose: compose.Compose{Secrets: compose.Secrets{
			"token":    {File: "./token", Data: []byte("secret")},
			"external": {External: true, Name: "external-token"},
		}},
	}

	changed, err := NewRotator().Rotate(file, "app", 8, false)

	require.NoError(t, err, "rotate secrets")
	assert.True(t, changed, "expected rotation mutation")
	assert.Equal(t, labelsdict.RotatedResourceManagedLabelValue,
		file.Compose.Secrets["token"].Labels.Map[labelsdict.RotatedResourceManagedLabelKey],
		"expected managed label")
	assert.Equal(t, "token",
		file.Compose.Secrets["token"].Labels.Map[labelsdict.RotatedResourceLogicalNameLabelKey],
		"expected logical name label")
	assert.Empty(t, file.Compose.Secrets["external"].Labels.Map, "external secret must not be managed")
}

func TestRotatorUsesLoadedContent(t *testing.T) {
	tests := []struct {
		name         string
		compose      compose.Compose
		expectedErr  string
		expectedName string
	}{
		{
			name: "config content",
			compose: compose.Compose{Configs: compose.Configs{
				"app": {File: "./app.conf", Data: []byte("current")},
			}},
			expectedName: NewRotator().buildRotatedObjectName("app", "app", "./app.conf", []byte("current"), 8, false),
		},
		{
			name: "secret content",
			compose: compose.Compose{Secrets: compose.Secrets{
				"app": {File: "./app.secret", Data: []byte("current")},
			}},
			expectedName: NewRotator().buildRotatedObjectName("app", "app", "./app.secret", []byte("current"), 8, false),
		},
		{
			name: "config content was not loaded",
			compose: compose.Compose{Configs: compose.Configs{
				"app": {File: "./app.conf"},
			}},
			expectedErr: "content of ./app.conf for rotation was not loaded",
		},
		{
			name: "secret content was not loaded",
			compose: compose.Compose{Secrets: compose.Secrets{
				"app": {File: "./app.secret"},
			}},
			expectedErr: "content of ./app.secret for rotation was not loaded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &compose.File{Path: filepath.Join(t.TempDir(), "compose.yaml"), Compose: tt.compose}

			configs, secrets, namesErr := NewRotator().DesiredResourceNames(file, "app", 8, false)
			_, rotateErr := NewRotator().Rotate(file, "app", 8, false)

			if tt.expectedErr != "" {
				require.ErrorContains(t, namesErr, tt.expectedErr)
				require.ErrorContains(t, rotateErr, tt.expectedErr)
				return
			}
			require.NoError(t, namesErr)
			require.NoError(t, rotateErr)
			names := configs
			if len(tt.compose.Secrets) > 0 {
				names = secrets
			}
			assert.Equal(t, map[string]string{"app": tt.expectedName}, names)
		})
	}
}
