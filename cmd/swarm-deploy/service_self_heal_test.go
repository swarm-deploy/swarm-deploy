package main

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"github.com/swarm-deploy/swarm-deploy/internal/usecase"
)

func TestServiceSelfHealEnablerWithoutAssistant(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	tree, err := repo.Worktree()
	require.NoError(t, err)
	const filename = "stack.yaml"
	const label = labelsdict.ServiceSyncPolicySelfHealLabelKey
	commit := func(raw string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(raw), 0o600))
		_, addErr := tree.Add(filename)
		require.NoError(t, addErr)
		_, commitErr := tree.Commit("test manifest", &gogit.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com"}})
		require.NoError(t, commitErr)
	}
	const initial = "services:\n  api: {image: app:v1}\n"
	commit(initial)
	base, err := repo.Head()
	require.NoError(t, err)
	cfg := &config.Config{Spec: config.Spec{
		DataDir: t.TempDir(),
		GitOps: config.GitOpsSpec{
			Stacks: config.StacksSourceSpec{File: "stacks.yaml"},
			Git:    config.GitSpec{Repository: "unused", Branch: "other", Push: &config.GitPushSpec{Repository: dir, Branch: base.Name().Short()}},
		},
	}}
	require.False(t, cfg.Spec.Assistant.Enabled)
	ctx := t.Context()
	catalog := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	cnt := &container{
		ConfigStore: configstore.New(cfg, &dispatcher.NopDispatcher{}),
		Resources:   &resources.Module{ServiceStore: catalog},
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte("stacks:\n  - name: core\n    composeFile: stack.yaml\n"), 0o600))
	require.NoError(t, cnt.ConfigStore.Reload(ctx, dir))
	require.NoError(t, initializeUseCases(ctx, cfg, cnt))
	enabler := cnt.ServiceSelfHealEnabler
	require.NoError(t, catalog.ReplaceStack(ctx, "core", []model.Info{{Stack: "core", Name: "api"}}))
	require.NoError(t, catalog.ReplaceStack(ctx, "backup", []model.Info{{Stack: "backup", Name: "api"}}))
	_, err = enabler.Prepare(ctx, "api")
	var ambiguous *usecase.AmbiguousServiceTargetError
	require.ErrorAs(t, err, &ambiguous)
	assert.Equal(t, []string{"backup/api", "core/api"}, ambiguous.Candidates)
	_, err = enabler.Prepare(ctx, "missing")
	require.ErrorIs(t, err, usecase.ErrServiceNotFound)

	plan, err := enabler.Prepare(ctx, "CORE/API")
	require.NoError(t, err)
	require.Equal(t, usecase.ServiceSelfHealPlan{Stack: "core", Service: "api", Policy: selfheal.State{Source: "global"}}, plan)
	require.NoError(t, catalog.ReplaceStack(ctx, "core", []model.Info{{Stack: "core", Name: "api-backup"}}))
	_, err = enabler.Publish(ctx, plan, "test")
	require.ErrorIs(t, err, usecase.ErrServiceNotFound)
	branches, err := repo.Branches()
	require.NoError(t, err)
	var names []string
	require.NoError(t, branches.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())
		return nil
	}))
	assert.Equal(t, []string{base.Name().Short()}, names)
	require.NoError(t, catalog.ReplaceStack(ctx, "core", []model.Info{{Stack: "core", Name: "api"}}))
	published, err := enabler.Publish(ctx, plan, "test")
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	assert.Equal(t, base.Hash(), head.Hash())
	proposal, err := repo.Reference(plumbing.NewBranchReferenceName(published.Branch), true)
	require.NoError(t, err)
	revision, err := repo.CommitObject(proposal.Hash())
	require.NoError(t, err)
	assert.Equal(t, "test", revision.Author.Name)
	file, err := revision.File(filename)
	require.NoError(t, err)
	body, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t, "services:\n  api: {deploy: {labels: {"+label+": \"true\"}}, image: app:v1}\n", body)
	commit("services:\n  api: {deploy: {labels: {" + label + ": 'false'}}}\n")
	_, err = enabler.Publish(ctx, plan, "test")
	require.ErrorIs(t, err, selfheal.ErrPolicyChanged)
	commit(body)
	already, err := enabler.Publish(ctx, plan, "test")
	require.NoError(t, err)
	assert.Equal(t, selfheal.Result{AlreadyEnabled: "service"}, already)
	enabledPlan, err := enabler.Prepare(ctx, "core/api")
	require.NoError(t, err)
	require.True(t, enabledPlan.Policy.Enabled)
	commit("services:\n  api: {deploy: {labels: {" + label + ": 'false'}}}\n")
	_, err = enabler.Publish(ctx, enabledPlan, "test")
	require.ErrorIs(t, err, selfheal.ErrPolicyChanged)
}
