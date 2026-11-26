package assistant

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

func TestDisableSelfHealThroughChat(t *testing.T) {
	const label = labelsdict.ServiceSyncPolicySelfHealLabelKey
	const inherited = "services:\n  backup: {image: backup:v1, deploy: {replicas: 2}}\n  api: {image: api:v1}\n"
	const overrideEnabled = "services:\n  backup:\n    image: backup:v1\n    deploy:\n      labels:\n        - " + label + "=true # keep\n  api: {image: api:v1}\n"
	const overrideDisabled = "services:\n  backup: {image: backup:v1, deploy: {labels: {" + label + ": 'false'}}}\n"
	const branch = "jobs-backup-disable-self-heal"

	for _, tc := range []struct {
		name, initial, update, language string
		first, confirmed, changed, want string
		allow                           []string
	}{
		{
			name: "inherited stack policy", language: "ru", initial: inherited,
			first: "включён политикой стека", confirmed: "после попадания в отслеживаемую ветку и синхронизации GitOps",
			want: strings.Replace(inherited, "replicas: 2", "replicas: 2, labels: {"+label+": 'false'}", 1),
		},
		{
			name: "already disabled", language: "en", initial: overrideDisabled,
			first: "is already disabled by its service label",
		},
		{
			name: "disabled since preparation", language: "en", initial: inherited, update: overrideDisabled,
			first: "is enabled by stack policy", confirmed: "is already disabled by its service label",
		},
		{
			name: "source changed since preparation", language: "en", initial: inherited, update: overrideEnabled,
			first: "is enabled by stack policy", changed: "is enabled by its service label",
			confirmed: "after it reaches the tracked branch and GitOps syncs it",
			want:      strings.Replace(overrideEnabled, label+"=true", label+"=false", 1),
		},
		{
			name: "allowlist only enables existing tool", language: "en", initial: inherited,
			first: "forbidden by `assistant.tools`", allow: []string{serviceSelfHealEnableTool},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"choices": []map[string]any{{"message": map[string]any{"content": `{"route":"services","operation":{"type":"service_self_heal_disable","target":"jobs/backup"}}`}}},
				}))
			}))
			defer server.Close()
			service, repo, commit := newSelfHealChat(t, server.URL, tc.language, tc.allow)
			base := commit(tc.initial)
			ref := plumbing.NewBranchReferenceName(branch)
			assertNoProposal := func() {
				t.Helper()
				_, err := repo.Reference(ref, true)
				require.ErrorIs(t, err, plumbing.ErrReferenceNotFound)
			}

			first := service.Chat(t.Context(), ChatRequest{Message: "Отключи self-heal у jobs/backup"})
			require.Equal(t, StatusCompleted, first.Status, first.Answer)
			assert.Contains(t, first.Answer, tc.first)
			assertNoProposal()
			if tc.confirmed == "" {
				assert.Nil(t, first.Selection)
				assert.EqualValues(t, 1, requests.Load())
				return
			}
			require.NotNil(t, first.Selection)
			assert.Contains(t, first.Answer, "`jobs/backup`")
			assert.Contains(t, first.Answer, label)
			assert.Contains(t, first.Answer, "`\"false\"`")
			if tc.update != "" {
				base = commit(tc.update)
			}

			confirm := ChatRequest{ConversationID: first.ConversationID, Message: "да"}
			response := service.Chat(t.Context(), confirm)
			require.Equal(t, StatusCompleted, response.Status, response.Answer)
			if tc.changed != "" {
				require.NotNil(t, response.Selection)
				assert.Contains(t, response.Answer, tc.changed)
				assertNoProposal()
				response = service.Chat(t.Context(), confirm)
				require.Equal(t, StatusCompleted, response.Status, response.Answer)
			}
			assert.Nil(t, response.Selection)
			assert.Contains(t, response.Answer, tc.confirmed)
			assert.EqualValues(t, 1, requests.Load(), "only the initial routing calls the model")
			if tc.want == "" {
				assertNoProposal()
				return
			}
			assert.Contains(t, response.Answer, branch)
			proposal, err := repo.Reference(ref, true)
			require.NoError(t, err)
			revision, err := repo.CommitObject(proposal.Hash())
			require.NoError(t, err)
			assert.Equal(t, []plumbing.Hash{base}, revision.ParentHashes)
			file, err := revision.File("stack.yaml")
			require.NoError(t, err)
			raw, err := file.Contents()
			require.NoError(t, err)
			assert.Equal(t, tc.want, raw)
			head, err := repo.Head()
			require.NoError(t, err)
			assert.Equal(t, base, head.Hash())
		})
	}
}

func newSelfHealChat(
	t *testing.T, baseURL, language string, allowed []string,
) (*Service, *gogit.Repository, func(string) plumbing.Hash) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	tree, err := repo.Worktree()
	require.NoError(t, err)
	base, err := repo.Reference(plumbing.HEAD, false)
	require.NoError(t, err)
	commit := func(raw string) plumbing.Hash {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(raw), 0o600))
		_, addErr := tree.Add("stack.yaml")
		require.NoError(t, addErr)
		hash, commitErr := tree.Commit("test manifest", &gogit.CommitOptions{
			Author: &object.Signature{Name: "test", Email: "test@example.com"},
		})
		require.NoError(t, commitErr)
		return hash
	}
	cfg := &config.Config{Spec: config.Spec{
		DataDir:   t.TempDir(),
		Assistant: config.AssistantSpec{Tools: allowed},
		GitOps: config.GitOpsSpec{
			Stacks: config.StacksSourceSpec{File: "stacks.yaml"},
			Git: config.GitSpec{
				Repository: "unused", Branch: "other",
				Push: &config.GitPushSpec{Repository: dir, Branch: base.Target().Short()},
			},
		},
	}}
	const stacks = "stacks:\n  - name: jobs\n    composeFile: stack.yaml\n    sync:\n      policy:\n        selfHeal: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(stacks), 0o600))
	_, err = cfg.Reload(dir)
	require.NoError(t, err)
	proposals, err := selfheal.New(cfg, cfg.Spec.GitOps.Sync.Policy.SelfHeal, cfg.Spec.GitOps.Git, cfg.Spec.DataDir)
	require.NoError(t, err)
	db := testutil.OpenSQLite(t)
	catalog := modelstore.NewSQLStore(db)
	require.NoError(t, catalog.ReplaceStack(t.Context(), "jobs", []model.Info{{Stack: "jobs", Name: "backup"}}))
	selfHeal := usecase.NewServiceSelfHealChanger(catalog, proposals)
	executor := tools.NewExecutor(&resources.Module{ServiceStore: catalog}, &gitops.Module{}, selfHeal, nil,
		&alertmanagement.Module{}, &event.Module{}, &swarm.Swarm{}, nil, nil, nil, nil, nil,
		metrics.NewGroup(metrics.CreateGroupParams{}).MCP, cfg.Spec)
	service, err := NewService(Config{
		Localizer: testAssistantTexts(t, language), Enabled: true,
		ModelName: "test-model", BaseURL: baseURL, APIToken: "test-token", MaxTokens: 200,
		AllowedTools: allowed, ConversationInMemoryTTL: time.Hour,
		ConversationHistory: conversation.NewSQLHistoryStorage(db),
		PrepareSelfHeal:     selfHeal.Prepare,
	}, catalog, executor, &dispatcher.NopDispatcher{}, metrics.NopAssistant{})
	require.NoError(t, err)
	return service, repo, commit
}
