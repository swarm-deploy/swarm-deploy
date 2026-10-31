package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// TestAgeSecretsOnRealSwarm runs the production GitOps reconciler, renderer,
// rotation, Docker API and actual tasks. No Docker APIs are mocked.
func TestAgeSecretsOnRealSwarm(t *testing.T) {
	if os.Getenv("SWARM_DEPLOY_E2E") != "1" {
		t.Skip("set SWARM_DEPLOY_E2E=1 to run against a real initialized Docker Swarm")
	}
	for _, rotation := range []bool{false, true} {
		t.Run(fmt.Sprintf("rotation=%t", rotation), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			dockerPath, err := exec.LookPath("docker")
			require.NoError(t, err)
			dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, dockerClient.Close()) })
			stack := fmt.Sprintf("sdage%d", time.Now().UnixNano())
			dir := t.TempDir()
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cleanupCancel()
				_, cleanupErr := exec.CommandContext(cleanupCtx, dockerPath, "stack", "rm", stack).CombinedOutput()
				assert.NoError(t, cleanupErr)
				assert.Eventually(t, func() bool {
					_, _, serviceErr := dockerClient.ServiceInspectWithRaw(
						cleanupCtx, stack+"_check", dockerswarm.ServiceInspectOptions{},
					)
					_, networkErr := dockerClient.NetworkInspect(
						cleanupCtx, stack+"_default", dockernetwork.InspectOptions{},
					)
					return errdefs.IsNotFound(serviceErr) && errdefs.IsNotFound(networkErr)
				}, 20*time.Second, 100*time.Millisecond, "stack resources must be removed before the next case")
			})
			identity, err := age.GenerateX25519Identity()
			require.NoError(t, err)
			identityPath := filepath.Join(dir, "identity")
			require.NoError(t, os.WriteFile(identityPath, []byte(identity.String()), 0o600))
			cfgPath := filepath.Join(dir, "swarm-deploy.yaml")
			require.NoError(t, os.WriteFile(cfgPath, fmt.Appendf(nil, `git:
  repository: https://example.com/fixture.git
sync:
  mode: pull
stacks:
  file: ./stacks.yaml
secretRotation:
  enabled: %t
  age:
    identityFile: %s
  cleanup:
    enabled: %t
    keepLast: 1
    minAge: 1ns
`, rotation, identityPath, rotation), 0o600))
			cfg, err := config.Load(cfgPath)
			require.NoError(t, err)
			db, err := storage.OpenSQLite(ctx, cfg.Spec.DataDir)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, db.Close()) })

			// Any Docker CLI invocation is a regression: deployments must use the API.
			wrapperPath := filepath.Join(dir, "docker-wrapper.sh")
			require.NoError(t, os.WriteFile(wrapperPath, []byte(`#!/bin/sh
echo "unexpected Docker CLI invocation: $*" >&2
exit 97
`), 0o700))
			swarmService := swarm.NewSwarm(dockerClient, wrapperPath)
			fileSystem := sharedfs.NewLocalFileSystem()
			stackDeployer := deployer.NewDeployer(100*time.Millisecond, time.Minute, dockerClient, swarmService, &metrics.NopDeploys{},
				deployer.NewSecretResolver(fileSystem, deployer.NewAgeDecoder(cfg.Spec.SecretRotation.Age.IdentityFile.Content), deployer.PlaintextDecoder{}))
			bus := eventbus.NewOutboxDispatcher(outbox.New(db))
			deployMetrics := &metrics.NopDeploys{}
			executor := deployments.NewController(
				deployments.NewService(db, bus), stackDeployer, swarmService.Services,
				pruner.NewServicePruner(swarmService.Services, bus, cfg.Spec.Sync.Policy), deployMetrics,
			)
			repository := gitx.NewRepository(cfg.Spec.Git, dir)
			rotator := stackloop.NewRotator(fileSystem)
			preparer := preparation.New(cfg, repository, fileSystem, rotator)
			r := stackloop.New(cfg, executor, swarmService, bus, deployMetrics,
				modelstore.NewSQLStore(db), preparer, rotator, db)
			req := stackloop.ReconciliationRequest{Stack: config.StackSpec{Name: stack, ComposeFile: "compose.yaml"}, Commit: "v1"}
			composePath := filepath.Join(dir, "compose.yaml")
			writeAgeSwarmFixture(t, dir, identity.Recipient(), "age-private-marker-v1", "mounted-age-v1")
			initialManifest, err := os.ReadFile(composePath)
			require.NoError(t, err)
			require.NoError(t, r.Reconcile(ctx, req))
			waitForRealSwarmTask(t, ctx, dockerPath, stack+"_check", "mounted-age-v1")
			firstName, firstID := inspectAgeSwarmSecret(t, ctx, dockerClient, stack)
			beforeInitFailure := inspectAgeSwarmService(t, ctx, dockerClient, stack)
			// A deliberately failing init job must prevent regular service deployment,
			// even though the same valid secret already exists in Swarm.
			brokenInit := strings.Replace(string(initialManifest), `command: ["test `, `command: ["false && test `, 1)
			require.NotEqual(t, string(initialManifest), brokenInit)
			require.NoError(t, os.WriteFile(composePath, []byte(brokenInit), 0o600))
			req.Commit = "init-failure"
			err = r.Reconcile(ctx, req)
			require.ErrorContains(t, err, "init job")
			afterInitFailure := inspectAgeSwarmService(t, ctx, dockerClient, stack)
			assert.Equal(t, beforeInitFailure.Version.Index, afterInitFailure.Version.Index)
			require.NoError(t, os.WriteFile(composePath, initialManifest, 0o600))
			req.Commit = "v1"

			// Unchanged reconcile must preserve the Docker secret identity.
			require.NoError(t, r.Reconcile(ctx, req))
			_, repeatedID := inspectAgeSwarmSecret(t, ctx, dockerClient, stack)
			assert.Equal(t, firstID, repeatedID)
			if rotation {
				for _, version := range []int{2, 3} {
					req.Commit = fmt.Sprintf("v%d", version)
					marker := fmt.Sprintf("mounted-age-v%d", version)
					writeAgeSwarmFixture(t, dir, identity.Recipient(), fmt.Sprintf("age-private-marker-v%d", version), marker)
					require.NoError(t, r.Reconcile(ctx, req))
					waitForRealSwarmTask(t, ctx, dockerPath, stack+"_check", marker)
					name, id := inspectAgeSwarmSecret(t, ctx, dockerClient, stack)
					assert.NotEqual(t, firstName, name)
					assert.NotEqual(t, firstID, id)
				}
				// Once the first generation is no longer desired or a rollback
				// reference, existing cleanup must remove it from the real Swarm.
				require.Eventually(t, func() bool {
					if r.Cleanup(ctx, req.Stack) != nil {
						return false
					}
					_, _, inspectErr := dockerClient.SecretInspectWithRaw(ctx, firstID)
					return errdefs.IsNotFound(inspectErr)
				}, 30*time.Second, time.Second)
			} else {
				// Docker secrets remain immutable without name rotation.
				req.Commit = "v2"
				writeAgeSwarmFixture(t, dir, identity.Recipient(), "age-private-marker-v2", "mounted-age-v2")
				require.NoError(t, os.WriteFile(composePath, initialManifest, 0o600))
				require.NoError(t, r.Reconcile(ctx, req))
				_, id := inspectAgeSwarmSecret(t, ctx, dockerClient, stack)
				assert.Equal(t, firstID, id)
			}

			// The original manifest and ciphertext remain untouched; persisted
			// desired state, SQLite/WAL and rendered files
			// must contain neither age plaintext nor the private key.
			assertAgeFilesSafe(t, dir, identityPath, identity.String())
			store := deploymentstore.NewStore(db)
			deployments, err := store.List(ctx, deploymentmodel.ListFilter{})
			require.NoError(t, err)
			require.NotEmpty(t, deployments.Deployments)
			for _, item := range deployments.Deployments {
				if item.Commit == "init-failure" {
					assert.Equal(t, deploymentmodel.Superseded, item.Status)
					failed, getErr := store.Get(ctx, item.ID)
					require.NoError(t, getErr)
					require.Len(t, failed.Attempts, 1)
					assert.Equal(t, deploymentmodel.Failed, failed.Attempts[0].Status)
				} else {
					assert.Equal(t, deploymentmodel.Succeeded, item.Status)
				}
			}
			original, err := os.ReadFile(composePath)
			require.NoError(t, err)
			assert.Contains(t, string(original), "file: ./password.age")

			// Invalid ciphertext must fail before mutating Docker and leave the
			// currently running stack and existing secrets intact.
			before, err := swarmService.Secrets.List(ctx, swarm.ListSecretsFilter{StackName: stack})
			require.NoError(t, err)
			serviceBeforeInvalid := inspectAgeSwarmService(t, ctx, dockerClient, stack)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "password.age"), []byte("invalid-ciphertext-marker"), 0o600))
			// Keep the same Compose name: validation must also run when Docker would reuse the Secret.
			req.Commit = "invalid"
			err = r.Reconcile(ctx, req)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "invalid-ciphertext-marker")
			after, err := swarmService.Secrets.List(ctx, swarm.ListSecretsFilter{StackName: stack})
			require.NoError(t, err)
			assert.Equal(t, before, after)
			serviceAfterInvalid := inspectAgeSwarmService(t, ctx, dockerClient, stack)
			assert.Equal(t, serviceBeforeInvalid.Version.Index, serviceAfterInvalid.Version.Index)
			assertAgeFilesSafe(t, dir, identityPath, identity.String())
		})
	}
}

func writeAgeSwarmFixture(t *testing.T, dir string, recipient age.Recipient, value, marker string) {
	t.Helper()
	var encrypted bytes.Buffer
	w, err := age.Encrypt(&encrypted, recipient)
	require.NoError(t, err)
	_, err = w.Write([]byte(value))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password.age"), encrypted.Bytes(), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("plain-fixture"), 0o600))
	manifest := fmt.Sprintf(`services:
  check:
    image: busybox:1.36
    command: ["sh", "-ec", "test $$(sha256sum /run/secrets/password | cut -d' ' -f1) = %x && grep -qx plain-fixture /run/secrets/plain && echo %s && sleep 300"]
    x-init-deploy-jobs:
      - name: validate-age
        image: busybox:1.36
        entrypoint: ["sh", "-ec"]
        command: ["test $(sha256sum /run/secrets/password | cut -d' ' -f1) = %x && grep -qx plain-fixture /run/secrets/plain"]
    secrets:
      - source: password
        target: password
        uid: "0"
        gid: "0"
        mode: 0400
      - plain
secrets:
  password:
    file: ./password.age
  plain:
    file: ./plain.txt
`, sha256.Sum256([]byte(value)), marker, sha256.Sum256([]byte(value)))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(manifest), 0o600))
}

func inspectAgeSwarmSecret(t *testing.T, ctx context.Context, docker *client.Client, stack string) (string, string) {
	t.Helper()
	service := inspectAgeSwarmService(t, ctx, docker, stack)
	refs := service.Spec.TaskTemplate.ContainerSpec.Secrets
	require.Len(t, refs, 2)
	for _, ref := range refs {
		if ref.File.Name != "password" {
			continue
		}
		secret, _, inspectErr := docker.SecretInspectWithRaw(ctx, ref.SecretID)
		require.NoError(t, inspectErr)
		assert.Equal(t, stack, secret.Spec.Labels["com.docker.stack.namespace"])
		assert.Equal(t, os.FileMode(0o400), ref.File.Mode)
		return ref.SecretName, ref.SecretID
	}
	t.Fatal("password mount not found")
	return "", ""
}

func inspectAgeSwarmService(t *testing.T, ctx context.Context, docker *client.Client, stack string) dockerswarm.Service {
	t.Helper()
	service, _, err := docker.ServiceInspectWithRaw(ctx, stack+"_check", dockerswarm.ServiceInspectOptions{})
	require.NoError(t, err)
	return service
}

func assertAgeFilesSafe(t *testing.T, dir, identityPath, privateKey string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == identityPath {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		assert.False(t, bytes.Contains(content, []byte("age-private-marker-")), "plaintext persisted in %s", path)
		assert.False(t, strings.Contains(string(content), privateKey), "private key persisted in %s", path)
		return nil
	}))
}
