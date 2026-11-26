package assistant

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
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
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/imagepin"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
	"go.uber.org/mock/gomock"
)

func TestPinRunningImageThroughChat(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.MarshalWrite(w, map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"content": `{"route":"services","operation":{"type":"service_image_pin","target":"prod/api"}}`,
			}}},
		}))
	}))
	defer server.Close()

	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	tree, err := repo.Worktree()
	require.NoError(t, err)
	const initial = "services:\n  api: {image: api:1.8, environment: [KEEP=yes]}\n  worker: {image: worker:2}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stack.yaml"), []byte(initial), 0o600))
	_, err = tree.Add("stack.yaml")
	require.NoError(t, err)
	base, err := tree.Commit("test manifest", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com"},
	})
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	cfg := &config.Config{Spec: config.Spec{
		DataDir:   t.TempDir(),
		Assistant: config.AssistantSpec{},
		GitOps: config.GitOpsSpec{
			Stacks: config.StacksSourceSpec{File: "stacks.yaml"},
			Git: config.GitSpec{Repository: "unused", Branch: "other", Push: &config.GitPushSpec{
				Repository: dir, Branch: head.Name().Short(),
			}},
		},
	}}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(
		"stacks:\n  - name: prod\n    composeFile: stack.yaml\n",
	), 0o600))
	_, err = cfg.Reload(dir)
	require.NoError(t, err)

	db := testutil.OpenSQLite(t)
	catalog := modelstore.NewSQLStore(db)
	require.NoError(t, catalog.ReplaceStack(t.Context(), "prod", []model.Info{{Stack: "prod", Name: "api"}}))
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().ListTasks(gomock.Any(), "prod_api").Return([]swarm.ServiceTask{
		{CurrentState: swarm.TaskStateRunning, Spec: swarm.NewTaskSpec(dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:1.8@" + digest},
		})},
	}, nil).Times(2)
	proposals, err := imagepin.New(cfg, cfg.Spec.GitOps.Git, cfg.Spec.DataDir)
	require.NoError(t, err)
	pinner := usecase.NewServiceImagePinner(catalog, manager, proposals)
	eventModule := &event.Module{Dispatcher: &dispatcher.NopDispatcher{}}
	executor := tools.NewExecutor(
		&resources.Module{ServiceStore: catalog}, &gitops.Module{}, nil, pinner, nil,
		&alertmanagement.Module{}, eventModule, &swarm.Swarm{Services: manager}, nil, nil, nil, nil, nil,
		metrics.NewGroup(metrics.CreateGroupParams{}).MCP, cfg.Spec,
	)
	service, err := NewService(Config{
		Localizer: testAssistantTexts(t, "ru"), Enabled: true,
		ModelName: "test-model", BaseURL: server.URL, APIToken: "test-token", MaxTokens: 200,
		ConversationInMemoryTTL: time.Hour,
		ConversationHistory:     conversation.NewSQLHistoryStorage(db),
		PrepareImagePin:         pinner.Prepare,
	}, catalog, executor, eventModule.Dispatcher, metrics.NopAssistant{})
	require.NoError(t, err)

	first := service.Chat(t.Context(), ChatRequest{Message: "Закрепи в Git образ, который сейчас запущен у prod/api"})
	require.Equal(t, StatusCompleted, first.Status, first.Answer)
	require.NotNil(t, first.Selection)
	assert.Contains(t, first.Answer, "`prod/api`")
	assert.Contains(t, first.Answer, "Запущенных задач: 1")
	assert.Contains(t, first.Answer, "`api:1.8`")
	assert.Contains(t, first.Answer, "`api:1.8@"+digest+"`")
	branches, err := repo.Branches()
	require.NoError(t, err)
	require.NoError(t, branches.ForEach(func(ref *plumbing.Reference) error {
		assert.Equal(t, head.Name(), ref.Name(), "preparation must not publish a branch")
		return nil
	}))

	confirmed := service.Chat(t.Context(), ChatRequest{ConversationID: first.ConversationID, Message: "да"})
	require.Equal(t, StatusCompleted, confirmed.Status, confirmed.Answer)
	assert.Nil(t, confirmed.Selection)
	assert.EqualValues(t, 1, requests.Load(), "confirmation must not call the model")
	require.Len(t, confirmed.Artifacts, 1)
	branchArtifact := confirmed.Artifacts[0]
	assert.Equal(t, conversation.ArtifactTypeGitBranch, branchArtifact.Type)
	assert.Regexp(t, regexp.MustCompile(`^prod-api-pin-image-.+`), branchArtifact.Label)
	history, found, err := service.GetChat(t.Context(), confirmed.ConversationID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, history.Messages)
	assert.Equal(t, confirmed.Artifacts, history.Messages[len(history.Messages)-1].Artifacts)
	proposal, err := repo.Reference(plumbing.NewBranchReferenceName(branchArtifact.Label), true)
	require.NoError(t, err)
	revision, err := repo.CommitObject(proposal.Hash())
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{base}, revision.ParentHashes)
	file, err := revision.File("stack.yaml")
	require.NoError(t, err)
	body, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t,
		"services:\n  api: {image: api:1.8@"+digest+", environment: [KEEP=yes]}\n  worker: {image: worker:2}\n",
		body,
	)
}
