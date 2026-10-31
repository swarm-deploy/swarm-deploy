package syncrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func writeStacksFile(path, stackName string) error {
	return writeStacks(path, []string{stackName})
}

func writeStacks(path string, stackNames []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "stacks:\n"
	for _, stackName := range stackNames {
		content += "  - name: " + stackName + "\n    composeFile: " + stackName + ".yaml\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func writeNetworksFile(path, networkName string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("networks:\n  - name: "+networkName+"\n"), 0o600)
}

func newSQLState(t *testing.T) (storage.Database, *modelstore.SQLStore) {
	t.Helper()
	db, err := storage.OpenSQLite(context.Background(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, modelstore.NewSQLStore(db)
}

func readSQLState(t *testing.T, store *modelstore.SQLStore) model.Runtime {
	t.Helper()
	state, err := store.Read(context.Background())
	require.NoError(t, err)
	return state
}
