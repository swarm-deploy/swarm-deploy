package compose

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
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

func TestEnvFilesYAML(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		paths   []string
		wantErr bool
	}{
		{name: "single path", source: "env_file: app.env", paths: []string{"app.env"}},
		{name: "absolute path", source: "env_file: /etc/app.env", paths: []string{"/etc/app.env"}},
		{name: "list", source: "env_file:\n  - default.env\n  - prod.env", paths: []string{"default.env", "prod.env"}},
		{name: "flow list", source: "env_file: [default.env, prod.env]", paths: []string{"default.env", "prod.env"}},
		{name: "mapping remains unsupported", source: "env_file:\n  path: app.env", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var service Service
			err := yaml.Unmarshal([]byte(tt.source), &service)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			paths := make([]string, 0, len(service.EnvFiles))
			for _, file := range service.EnvFiles {
				paths = append(paths, file.Path)
			}
			assert.Equal(t, tt.paths, paths)

			// Marshal back to the established list representation.
			encoded, err := yaml.Marshal(service.EnvFiles)
			require.NoError(t, err)
			var decoded []string
			require.NoError(t, yaml.Unmarshal(encoded, &decoded))
			assert.Equal(t, tt.paths, decoded)
		})
	}
}

func TestComposeSingleEnvFile(t *testing.T) {
	var compose Compose
	err := yaml.Unmarshal([]byte("services:\n  app:\n    image: nginx:latest\n    env_file: app.env\n"), &compose)
	require.NoError(t, err)
	require.Len(t, compose.Services, 1)
	require.Len(t, compose.Services[0].EnvFiles, 1)
	assert.Equal(t, "app.env", compose.Services[0].EnvFiles[0].Path)
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
					EnvFiles: EnvFiles{
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
					EnvFiles: EnvFiles{
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
					EnvFiles: EnvFiles{{
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
