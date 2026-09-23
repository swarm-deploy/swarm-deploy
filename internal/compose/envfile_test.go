package compose

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvFilePopulatorPopulate(t *testing.T) {
	ctx := context.Background()
	baseDir := filepath.Join("repo", "deploy")
	composePath := filepath.Join(baseDir, "compose.yaml")

	files := map[string][]byte{
		filepath.Join(baseDir, "default.env"): []byte("FOO=default\nBAR=default\nEMPTY=\n"),
		filepath.Join(baseDir, "prod.env"):    []byte("FOO=prod\nBAZ=prod\n"),
	}

	reader := func(_ context.Context, path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, errors.New("file not found")
		}
		return content, nil
	}

	populator := NewEnvFilePopulator(reader)
	file := &File{
		Path: composePath,
		Compose: Compose{
			Services: Services{
				{
					Name:     "api",
					EnvFiles: []string{"default.env", "prod.env"},
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

	changed, err := populator.Populate(ctx, file)

	require.NoError(t, err)
	assert.True(t, changed)
	require.Len(t, file.Compose.Services, 1)

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
	reader := func(_ context.Context, path string) ([]byte, error) {
		switch filepath.Base(path) {
		case "first.env":
			return []byte("FOO=first\n"), nil
		case "second.env":
			return []byte("FOO=second\n"), nil
		default:
			return nil, errors.New("unexpected path")
		}
	}

	populator := NewEnvFilePopulator(reader)
	file := &File{
		Path: filepath.Join("repo", "compose.yaml"),
		Compose: Compose{
			Services: Services{
				{
					Name:     "api",
					EnvFiles: []string{"first.env", "second.env"},
				},
			},
		},
	}

	changed, err := populator.Populate(context.Background(), file)

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "second", file.Compose.Services[0].Environment.Map["FOO"])
}

func TestEnvFilePopulatorExplicitEnvironmentWins(t *testing.T) {
	reader := func(context.Context, string) ([]byte, error) {
		return []byte("FOO=from-file\n"), nil
	}

	populator := NewEnvFilePopulator(reader)
	file := &File{
		Path: filepath.Join("repo", "compose.yaml"),
		Compose: Compose{
			Services: Services{
				{
					Name:     "api",
					EnvFiles: []string{"app.env"},
					Environment: Environment{
						Map: map[string]string{
							"FOO": "explicit",
						},
					},
				},
			},
		},
	}

	_, err := populator.Populate(context.Background(), file)

	require.NoError(t, err)
	assert.Equal(t, "explicit", file.Compose.Services[0].Environment.Map["FOO"])
}

func TestEnvFilePopulatorKeepsServiceWithoutEnvFilesUntouched(t *testing.T) {
	populator := NewEnvFilePopulator(func(context.Context, string) ([]byte, error) {
		t.Fatal("reader must not be called")
		return nil, nil
	}, nil)

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

	changed, err := populator.Populate(context.Background(), file)

	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, map[string]string{"FOO": "bar"}, file.Compose.Services[0].Environment.Map)
}

func TestParseEnvFile(t *testing.T) {
	values, err := parseEnvFile([]byte("\xef\xbb\xbf# comment\n  FOO=bar baz  \nEMPTY=\n"))

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"EMPTY": "",
		"FOO":   "bar baz  ",
	}, values)
}

func TestParseEnvFileRejectsBareVariable(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "must-not-leak")

	values, err := parseEnvFile([]byte("SECRET_TOKEN\n"))

	require.Error(t, err)
	assert.Nil(t, values)
	assert.Contains(t, err.Error(), "must have an explicit value")
}

func TestParseEnvFileRejectsInvalidKey(t *testing.T) {
	_, err := parseEnvFile([]byte("BAD KEY=value\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains whitespace")
}
