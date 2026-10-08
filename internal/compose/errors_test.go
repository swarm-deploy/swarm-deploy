package compose

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func loadFrom(files map[string]string) (*File, error) {
	loader := NewFileLoaderWithReader(func(_ context.Context, path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}
		return []byte(content), nil
	})
	return loader.Load(context.Background(), "/stack/compose.yaml")
}

func TestFileLoaderReadComposeError(t *testing.T) {
	t.Parallel()

	_, err := loadFrom(nil)

	var readErr *ReadComposeError
	require.ErrorAs(t, err, &readErr)
	assert.Equal(t, "/stack/compose.yaml", readErr.FilePath)

	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFileLoaderParseComposeError(t *testing.T) {
	t.Parallel()

	_, err := loadFrom(map[string]string{"/stack/compose.yaml": "services: [unclosed\n  a: b: c"})

	var parseErr *ParseComposeError
	require.ErrorAs(t, err, &parseErr)
	assert.Equal(t, "/stack/compose.yaml", parseErr.FilePath)

	var validateErr *ValidateComposeError
	assert.False(t, errors.As(err, &validateErr))
}

func TestFileLoaderValidateComposeErrorInitJob(t *testing.T) {
	t.Parallel()

	_, err := loadFrom(map[string]string{"/stack/compose.yaml": `
services:
  app:
    image: nginx
    x-init-deploy-jobs:
      - command: ["true"]
`})

	var validateErr *ValidateComposeError
	require.ErrorAs(t, err, &validateErr)
	var parseErr *ParseComposeError
	assert.False(t, errors.As(err, &parseErr))
	require.NotEmpty(t, validateErr.Issues)
	issue := validateErr.Issues[0]
	assert.Equal(t, "service", issue.ResourceType)
	assert.Equal(t, "app", issue.ResourceName)
	assert.Equal(t, "init-jobs[0].image", issue.Field)
	assert.Equal(t, IssueCodeRequired, issue.Code)
	assert.Equal(t,
		`validate compose file "/stack/compose.yaml": service "app": init-jobs[0].image: image is required`,
		validateErr.Error(),
	)
	assert.NoError(t, validateErr.Err)
}

func TestFileLoaderValidateComposeErrorWrongType(t *testing.T) {
	t.Parallel()

	_, err := loadFrom(map[string]string{"/stack/compose.yaml": "services:\n  app:\n    image: [a, b]\n"})

	var validateErr *ValidateComposeError
	require.ErrorAs(t, err, &validateErr)
	require.NotEmpty(t, validateErr.Issues)
	assert.NotEmpty(t, validateErr.Issues[0].Message)

	var typeErr *yaml.TypeError
	require.ErrorAs(t, err, &typeErr)
	assert.True(t, strings.HasPrefix(validateErr.Error(), `validate compose file "/stack/compose.yaml": compose: line`), validateErr.Error())
}

func TestFileLoaderKeepsIOErrorForReferencedFiles(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"env_file": "services:\n  app:\n    image: nginx\n    env_file: [missing.env]\n",
		"config":   "services:\n  app:\n    image: nginx\nconfigs:\n  c:\n    file: missing.conf\n",
		"secret":   "services:\n  app:\n    image: nginx\nsecrets:\n  s:\n    file: missing.txt\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := loadFrom(map[string]string{"/stack/compose.yaml": content})

			require.ErrorIs(t, err, fs.ErrNotExist)
			var readErr *ReadComposeError
			assert.False(t, errors.As(err, &readErr), "only the main file is ReadComposeError")
		})
	}
}

func TestComposeErrorsWrap(t *testing.T) {
	t.Parallel()

	_, err := loadFrom(nil)
	wrapped := fmt.Errorf("sync: %w", err)

	var readErr *ReadComposeError
	require.ErrorAs(t, wrapped, &readErr)
	assert.Contains(t, wrapped.Error(), "/stack/compose.yaml")
}
