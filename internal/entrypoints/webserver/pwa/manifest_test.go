package pwa

import (
	"encoding/json/v2"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestPrepareLocalizedShortcuts(t *testing.T) {
	uiFS := fstest.MapFS{
		ManifestFile: {Data: []byte(`{
			"name": "Swarm Deploy",
			"short_name": "Swarm Deploy",
			"description": "GitOps controller for Docker Swarm",
			"start_url": "./",
			"scope": "./",
			"display": "standalone",
			"theme_color": "#141416",
			"background_color": "#141416",
			"icons": []
		}`)},
	}

	for _, tc := range []struct {
		locale string
		names  [2]string
	}{
		{locale: "en", names: [2]string{"Ask the assistant", "Events & Alerts"}},
		{locale: "ru", names: [2]string{"Спросить ассистента", "События и алерты"}},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			encoded, err := Prepare(uiFS, tc.locale, config.WebPWASpec{Name: "My Cluster", ShortName: "mc"})
			require.NoError(t, err)

			var manifest Manifest
			require.NoError(t, json.Unmarshal(encoded, &manifest))
			assert.Equal(t, "My Cluster", manifest.Name)
			assert.Equal(t, "mc", manifest.ShortName)
			require.Len(t, manifest.Shortcuts, 2)
			assert.Equal(t, tc.names[0], manifest.Shortcuts[0].Name)
			assert.Equal(t, tc.names[1], manifest.Shortcuts[1].Name)
		})
	}
}
