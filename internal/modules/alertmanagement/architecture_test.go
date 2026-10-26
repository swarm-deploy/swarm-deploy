package alertmanagement

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionPackageDoesNotDependOnGitOpsOrDeploymentInternals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	forbidden := []string{
		"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops",
		"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments",
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, parseErr, file)
		for _, imported := range parsed.Imports {
			path, unquoteErr := strconv.Unquote(imported.Path.Value)
			require.NoError(t, unquoteErr, file)
			for _, prefix := range forbidden {
				assert.False(t, strings.HasPrefix(path, prefix), "%s imports forbidden dependency %s", file, path)
			}
		}
	}
}
