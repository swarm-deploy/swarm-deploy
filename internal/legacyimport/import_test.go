package legacyimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	gitmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	gitstore "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	recommendationmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/model"
	recommendationstore "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/node"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	servicemodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func writeFixture(t *testing.T, dir, name string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, payload, 0o600))
}

func fixture(t *testing.T, dir string) snapshot {
	t.Helper()
	now := time.Date(2025, 2, 3, 4, 5, 6, 123456789, time.UTC)
	data := snapshot{
		runtime: gitmodel.Runtime{GitRevision: "commit", Stacks: map[string]gitmodel.Stack{"app": {LastCommit: "commit", Services: map[string]gitmodel.Service{}}}, Networks: map[string]gitmodel.Network{}},
		history: []history.Entry{
			{ID: "event-z", Type: events.TypeDeploySuccess, CreatedAt: now, Message: "deployed"},
			{ID: "event-a", Type: events.TypeDeploySuccess, CreatedAt: now.Add(-time.Hour), Message: "older timestamp, later sequence"},
		},
		alerts:          []alertmodel.Alert{{ID: "alert", Fingerprint: "app", Status: alertmodel.AlertStatusOpen, OpenedAt: now, UpdatedAt: now}},
		recommendations: []recommendationmodel.Recommendation{{Type: recommendationmodel.TypeServiceImageLatest, Subject: recommendationmodel.Subject{Stack: "app", Service: "api"}, CreatedAt: now}},
		nodes:           []swarm.Node{{ID: "node", Hostname: "manager"}},
		services:        []servicemodel.Info{{Stack: "app", Name: "api", Image: "api:1"}},
		secrets:         []secretmodel.Secret{{ID: "secret", Name: "api-token", CreatedAt: now, UpdatedAt: now}},
		chats:           []conversation.Chat{{ID: "chat", Title: "Hello", CreatedAt: now, UpdatedAt: now, Turns: []conversation.Turn{{Role: "user", Content: "Hello"}, {Role: "assistant", Content: "Hi"}}, Usage: &conversation.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}},
	}
	writeFixture(t, dir, "controller.state.json", data.runtime)
	writeFixture(t, dir, "event-history.json", data.history)
	writeFixture(t, dir, "alerts.state.json", map[string]any{"alerts": data.alerts})
	writeFixture(t, dir, "recommendations.state.json", map[string]any{"list": data.recommendations})
	writeFixture(t, dir, "nodes.json", data.nodes)
	writeFixture(t, dir, "secrets.state.json", map[string]any{"secrets": data.secrets})
	services, err := servicestore.NewFileStore(context.Background(), filepath.Join(dir, "services.json"), fs.NewLocalFileSystem())
	require.NoError(t, err)
	require.NoError(t, services.ReplaceStack(context.Background(), "app", data.services))
	chat := data.chats[0]
	hash := sha256.Sum256([]byte(chat.ID))
	writeFixture(t, dir, filepath.Join("assistant", "chats", hex.EncodeToString(hash[:])+".json"), chat)
	writeFixture(t, dir, "assistant/chats/index.json", map[string]any{"chats": []conversation.ChatSummary{{ID: chat.ID, Title: chat.Title, CreatedAt: chat.CreatedAt, UpdatedAt: chat.UpdatedAt, Usage: chat.Usage}}})
	return data
}

func TestImportPreservesAllStoresAndRestarts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	expected := fixture(t, dir)
	originals := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload, readErr := os.ReadFile(path)
		originals[path] = payload
		return readErr
	}))
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, Run(ctx, db, dir))
	state, err := gitstore.NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, expected.runtime, state)
	h, err := history.NewSQLStore(db, 1)
	require.NoError(t, err)
	entries, err := h.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, expected.history, entries, "import must not truncate history")
	alerts, err := alertstore.NewSQLStore(db).List(ctx, alertstore.ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, expected.alerts, alerts)
	recs, err := recommendationstore.NewSQLStore(db).List(ctx, recommendationstore.ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, expected.recommendations, recs)
	nodes, err := node.NewSQLStore(db).ReadAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, expected.nodes, nodes)
	secrets, err := secretstore.NewSQLStore(db).List(ctx)
	require.NoError(t, err)
	assert.Equal(t, expected.secrets, secrets)
	services, err := servicestore.NewSQLStore(db).ReadAll(ctx)
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, "api", services[0].Name)
	chat, ok, err := conversation.NewSQLHistoryStorage(db).ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, expected.chats[0], chat)
	require.NoError(t, db.Close())
	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, Run(ctx, db, dir))
	for path, payload := range originals {
		actual, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		assert.Equal(t, payload, actual, path)
	}
	// A completed marker prevents a second import, even if a backup is later damaged.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nodes.json"), []byte("{"), 0o600))
	require.NoError(t, Run(ctx, db, dir))
	nodes, err = node.NewSQLStore(db).ReadAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, expected.nodes, nodes)
}

func TestImportFailureIsAtomicAndRetryable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, string, *storage.Database)
		repair  func(*testing.T, string, *storage.Database)
	}{
		{"malformed_last_file", func(t *testing.T, dir string, _ *storage.Database) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "assistant/chats/index.json"), []byte("{"), 0o600))
		}, func(t *testing.T, dir string, _ *storage.Database) { fixture(t, dir) }},
		{"duplicate_node", func(t *testing.T, dir string, _ *storage.Database) {
			writeFixture(t, dir, "nodes.json", []swarm.Node{{ID: "same"}, {ID: "same"}})
		}, func(t *testing.T, dir string, _ *storage.Database) { fixture(t, dir) }},
		{"sql_failure_after_writes", func(t *testing.T, _ string, db *storage.Database) {
			_, err := db.Get(context.Background()).ExecContext(context.Background(), "CREATE TRIGGER reject_chat BEFORE INSERT ON assistant_chats BEGIN SELECT RAISE(ABORT, 'injected failure'); END")
			require.NoError(t, err)
		}, func(t *testing.T, _ string, db *storage.Database) {
			_, err := db.Get(context.Background()).ExecContext(context.Background(), "DROP TRIGGER reject_chat")
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			fixture(t, dir)
			db, err := storage.Open(ctx, dir)
			require.NoError(t, err)
			defer db.Close()
			tc.prepare(t, dir, db)
			require.Error(t, Run(ctx, db, dir))
			require.NoError(t, requireEmpty(ctx, db), "all imported rows must roll back")
			done, err := completed(ctx, db)
			require.NoError(t, err)
			assert.False(t, done)
			tc.repair(t, dir, db)
			require.NoError(t, Run(ctx, db, dir))
		})
	}
}

func TestImportRefusesUnmarkedNonemptyDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, node.NewSQLStore(db).ReplaceSnapshot(ctx, []swarm.Node{{ID: "existing"}}))
	require.ErrorContains(t, Run(ctx, db, dir), "refusing to overwrite")
	nodes, err := node.NewSQLStore(db).ReadAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, "existing", nodes[0].ID)
}
