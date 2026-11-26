package usecase

import (
	"os"
	"path/filepath"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/imagepin"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

const (
	imageDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	imageDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestAssessServiceImage(t *testing.T) {
	for _, tc := range []struct {
		name, gitImage string
		tasks          []swarm.ServiceTask
		status         ServiceImagePinStatus
		proposed       string
		digests        []ImageCount
		invalid        int
	}{
		{
			name: "ready", gitImage: "api:1.8",
			tasks: []swarm.ServiceTask{
				imagePinTask("docker.io/library/api:1.8@"+imageDigestA, swarm.TaskStateRunning),
				imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning),
				imagePinTask("api:old@"+imageDigestB, swarm.TaskStateShutdown),
			},
			status: ServiceImagePinReady, proposed: "api:1.8@" + imageDigestA,
		},
		{
			name: "already pinned", gitImage: "api:1.8@" + imageDigestA,
			tasks:  []swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning)},
			status: ServiceImagePinAlreadyPinned,
		},
		{
			name: "mixed rollout", gitImage: "api:1.8",
			tasks: []swarm.ServiceTask{
				imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning),
				imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning),
				imagePinTask("api:1.8@"+imageDigestB, swarm.TaskStateRunning),
			},
			status:  ServiceImagePinMixedRollout,
			digests: []ImageCount{{Value: imageDigestA, Count: 2}, {Value: imageDigestB, Count: 1}},
		},
		{name: "no running tasks", gitImage: "api:1.8", status: ServiceImagePinNoRunningTasks},
		{
			name: "digest unavailable", gitImage: "api:1.8",
			tasks: []swarm.ServiceTask{
				imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning),
				imagePinTask("api:1.8", swarm.TaskStateRunning),
			},
			status: ServiceImagePinDigestUnavailable, invalid: 1,
		},
		{
			name: "repository mismatch", gitImage: "other/api:1.8",
			tasks:  []swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning)},
			status: ServiceImagePinGitMismatch,
		},
		{
			name: "tag mismatch", gitImage: "api:1.9",
			tasks:  []swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning)},
			status: ServiceImagePinGitMismatch,
		},
		{
			name: "different pinned digest", gitImage: "api:1.8@" + imageDigestB,
			tasks:  []swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning)},
			status: ServiceImagePinGitMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := assessServiceImage("prod", "api", tc.gitImage, tc.tasks)
			require.NoError(t, err)
			assert.Equal(t, tc.status, actual.Status)
			assert.Equal(t, tc.proposed, actual.Plan.ProposedImage)
			if tc.digests != nil {
				assert.Equal(t, tc.digests, actual.DigestCounts)
			}
			assert.Equal(t, tc.invalid, actual.InvalidTasks)
		})
	}
}

func TestServiceImagePinnerPublishesOnlySelectedImage(t *testing.T) {
	repo, cfg, catalog, commit := setupImagePinRepo(t)
	base := commit("services:\n  api: {image: api:1.8, environment: [KEEP=yes]}\n  worker: {image: worker:2}\n")
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().ListTasks(gomock.Any(), "prod_api").Return([]swarm.ServiceTask{
		imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning),
		imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning),
	}, nil).Times(2)
	proposals, err := imagepin.New(cfg, cfg.Spec.GitOps.Git, cfg.Spec.DataDir)
	require.NoError(t, err)
	pinner := NewServiceImagePinner(catalog, manager, proposals)

	plan, err := pinner.Prepare(t.Context(), "prod", "api")
	require.NoError(t, err)
	require.Equal(t, ServiceImagePinReady, plan.Status)
	assertNoImagePinBranch(t, repo)
	result, err := pinner.Publish(t.Context(), plan.Plan, "test")
	require.NoError(t, err)

	ref, err := repo.Reference(plumbing.NewBranchReferenceName(result.Branch), true)
	require.NoError(t, err)
	revision, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{base}, revision.ParentHashes)
	file, err := revision.File("stack.yaml")
	require.NoError(t, err)
	body, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t,
		"services:\n  api: {image: api:1.8@"+imageDigestA+", environment: [KEEP=yes]}\n  worker: {image: worker:2}\n",
		body,
	)
	head, err := repo.Head()
	require.NoError(t, err)
	assert.Equal(t, base, head.Hash())
}

func TestServiceImagePinnerPublishesWithPreviousBranchRetained(t *testing.T) {
	repo, cfg, catalog, commit := setupImagePinRepo(t)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	proposals, err := imagepin.New(cfg, cfg.Spec.GitOps.Git, cfg.Spec.DataDir)
	require.NoError(t, err)
	pinner := NewServiceImagePinner(catalog, manager, proposals)
	pin := func(image, digest string) *plumbing.Reference {
		t.Helper()
		commit("services:\n  api: {image: " + image + "}\n")
		manager.EXPECT().ListTasks(gomock.Any(), "prod_api").Return([]swarm.ServiceTask{
			imagePinTask(image+"@"+digest, swarm.TaskStateRunning),
		}, nil).Times(2)
		assessment, err := pinner.Prepare(t.Context(), "prod", "api")
		require.NoError(t, err)
		require.Equal(t, ServiceImagePinReady, assessment.Status)
		result, err := pinner.Publish(t.Context(), assessment.Plan, "test")
		require.NoError(t, err)
		ref, err := repo.Reference(plumbing.NewBranchReferenceName(result.Branch), true)
		require.NoError(t, err)
		return ref
	}

	first := pin("api:1.8", imageDigestA)
	// Squash the first pin into the base, retaining its proposal branch.
	commit("services:\n  api: {image: api:1.8@" + imageDigestA + "}\n")
	second := pin("api:1.9", imageDigestB)
	require.NotEqual(t, first.Name(), second.Name())
	retained, err := repo.Reference(first.Name(), true)
	require.NoError(t, err)
	assert.Equal(t, first.Hash(), retained.Hash())
}

func TestServiceImagePinnerRejectsChangedRunningDigest(t *testing.T) {
	repo, cfg, catalog, commit := setupImagePinRepo(t)
	commit("services:\n  api: {image: api:1.8}\n")
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().ListTasks(gomock.Any(), "prod_api").Return(
		[]swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning)}, nil,
	)
	manager.EXPECT().ListTasks(gomock.Any(), "prod_api").Return(
		[]swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestB, swarm.TaskStateRunning)}, nil,
	)
	proposals, err := imagepin.New(cfg, cfg.Spec.GitOps.Git, cfg.Spec.DataDir)
	require.NoError(t, err)
	pinner := NewServiceImagePinner(catalog, manager, proposals)
	assessment, err := pinner.Prepare(t.Context(), "prod", "api")
	require.NoError(t, err)
	_, err = pinner.Publish(t.Context(), assessment.Plan, "test")
	require.ErrorIs(t, err, ErrImagePinPlanChanged)
	assertNoImagePinBranch(t, repo)
}

func TestServiceImagePinnerRecognizesConcurrentConfirmedChange(t *testing.T) {
	_, cfg, catalog, commit := setupImagePinRepo(t)
	commit("services:\n  api: {image: api:1.8}\n")
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().ListTasks(gomock.Any(), "prod_api").Return(
		[]swarm.ServiceTask{imagePinTask("api:1.8@"+imageDigestA, swarm.TaskStateRunning)}, nil,
	).Times(2)
	proposals, err := imagepin.New(cfg, cfg.Spec.GitOps.Git, cfg.Spec.DataDir)
	require.NoError(t, err)
	pinner := NewServiceImagePinner(catalog, manager, proposals)
	assessment, err := pinner.Prepare(t.Context(), "prod", "api")
	require.NoError(t, err)
	commit("services:\n  api: {image: api:1.8@" + imageDigestA + "}\n")
	result, err := pinner.Publish(t.Context(), assessment.Plan, "test")
	require.NoError(t, err)
	assert.True(t, result.AlreadyPinned)
}

func imagePinTask(image string, state swarm.TaskState) swarm.ServiceTask {
	return swarm.ServiceTask{CurrentState: state, Spec: swarm.NewTaskSpec(dockerswarm.TaskSpec{
		ContainerSpec: &dockerswarm.ContainerSpec{Image: image},
	})}
}

func setupImagePinRepo(
	t *testing.T,
) (*gogit.Repository, *config.Config, modelstore.Store, func(string) plumbing.Hash) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	tree, err := repo.Worktree()
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
	base, err := repo.Reference(plumbing.HEAD, false)
	require.NoError(t, err)
	cfg := &config.Config{Spec: config.Spec{
		DataDir: t.TempDir(),
		GitOps: config.GitOpsSpec{
			Stacks: config.StacksSourceSpec{File: "stacks.yaml"},
			Git: config.GitSpec{Repository: "unused", Branch: "other", Push: &config.GitPushSpec{
				Repository: dir, Branch: base.Target().Short(),
			}},
		},
	}}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(
		"stacks:\n  - name: prod\n    composeFile: stack.yaml\n",
	), 0o600))
	_, err = cfg.Reload(dir)
	require.NoError(t, err)
	catalog := modelstore.NewSQLStore(testutil.OpenSQLite(t))
	require.NoError(t, catalog.ReplaceStack(t.Context(), "prod", []model.Info{{Stack: "prod", Name: "api"}}))
	return repo, cfg, catalog, commit
}

func assertNoImagePinBranch(t *testing.T, repo *gogit.Repository) {
	t.Helper()
	branches, err := repo.Branches()
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	require.NoError(t, branches.ForEach(func(ref *plumbing.Reference) error {
		assert.Equal(t, head.Name(), ref.Name())
		return nil
	}))
}
