package preparation

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestNormalizeBindMountPathsPreparesRelativeSourcesForNativeConversion(t *testing.T) {
	services := compose.Services{{Volumes: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{
		{Type: compose.ServiceVolumeTypeBind, Source: "./data", Target: "/data"},
		{Type: compose.ServiceVolumeTypeBind, Source: "/srv/cache", Target: "/cache"},
		{Type: compose.ServiceVolumeTypeVolume, Source: "data", Target: "/volume"},
	}}}}
	baseDir := filepath.Join(string(filepath.Separator), "repo", "deploy")

	normalizeBindMountPaths(baseDir, services)

	assert.Equal(t, filepath.Join(baseDir, "data"), services[0].Volumes.Volumes[0].Source)
	assert.Equal(t, "/srv/cache", services[0].Volumes.Volumes[1].Source)
	assert.Equal(t, "data", services[0].Volumes.Volumes[2].Source)
}
