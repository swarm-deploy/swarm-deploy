package compose

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestEnvFileYAML(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "relative path", path: "config/app.env"},
		{name: "absolute path", path: "/etc/app.env"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var envFile EnvFile
			err := yaml.Unmarshal([]byte(tt.path), &envFile)
			require.NoError(t, err)
			assert.Equal(t, tt.path, envFile.Path)

			envFile.Variables = map[string]string{"TOKEN": "secret"}
			encoded, err := yaml.Marshal(envFile)
			require.NoError(t, err)
			assert.Equal(t, tt.path+"\n", string(encoded))
		})
	}
}

func TestEnvFilePopulatorPopulate(t *testing.T) {
	baseDir := filepath.Join("repo", "deploy")
	composePath := filepath.Join(baseDir, "compose.yaml")

	populator := NewEnvFilePopulator()
	file := &File{
		Path: composePath,
		Compose: Compose{
			Services: Services{
				{
					Name: "api",
					EnvFiles: []EnvFile{
						{
							Path: "default.env",
							Variables: map[string]string{
								"FOO":   "default",
								"BAR":   "default",
								"EMPTY": "",
							},
						},
						{
							Path: "prod.env",
							Variables: map[string]string{
								"FOO": "prod",
								"BAZ": "prod",
							},
						},
					},
					Environment: Environment{
						Map: map[string]string{
							"FOO":      "explicit",
							"EXPLICIT": "value",
						},
					},
				},
			},
		},
	}

	changed := populator.Populate(file)

	assert.True(t, changed)
	assert.Len(t, file.Compose.Services, 1)

	service := file.Compose.Services[0]
	assert.Nil(t, service.EnvFiles)
	assert.Equal(t, map[string]string{
		"BAR":      "default",
		"BAZ":      "prod",
		"EMPTY":    "",
		"EXPLICIT": "value",
		"FOO":      "explicit",
	}, service.Environment.Map)
	assert.Equal(t, []string{
		"BAR",
		"BAZ",
		"EMPTY",
		"EXPLICIT",
		"FOO",
	}, service.Environment.Keys)
}

func TestEnvFilePopulatorLaterEnvFileWins(t *testing.T) {
	populator := NewEnvFilePopulator()
	file := &File{
		Path: filepath.Join("repo", "compose.yaml"),
		Compose: Compose{
			Services: Services{
				{
					Name: "api",
					EnvFiles: []EnvFile{
						{Path: "first.env", Variables: map[string]string{"FOO": "first"}},
						{Path: "second.env", Variables: map[string]string{"FOO": "second"}},
					},
				},
			},
		},
	}

	changed := populator.Populate(file)

	assert.True(t, changed)
	assert.Equal(t, "second", file.Compose.Services[0].Environment.Map["FOO"])
}

func TestEnvFilePopulatorExplicitEnvironmentWins(t *testing.T) {
	populator := NewEnvFilePopulator()
	file := &File{
		Path: filepath.Join("repo", "compose.yaml"),
		Compose: Compose{
			Services: Services{
				{
					Name: "api",
					EnvFiles: []EnvFile{{
						Path:      "app.env",
						Variables: map[string]string{"FOO": "from-file"},
					}},
					Environment: Environment{
						Map: map[string]string{
							"FOO": "explicit",
						},
					},
				},
			},
		},
	}

	populator.Populate(file)

	assert.Equal(t, "explicit", file.Compose.Services[0].Environment.Map["FOO"])
}

func TestEnvFilePopulatorKeepsServiceWithoutEnvFilesUntouched(t *testing.T) {
	populator := NewEnvFilePopulator()

	file := &File{
		Path: "compose.yaml",
		Compose: Compose{
			Services: Services{
				{
					Name: "api",
					Environment: Environment{
						Map: map[string]string{"FOO": "bar"},
					},
				},
			},
		},
	}

	changed := populator.Populate(file)

	assert.False(t, changed)
	assert.Equal(t, map[string]string{"FOO": "bar"}, file.Compose.Services[0].Environment.Map)
}
