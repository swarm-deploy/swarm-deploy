package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/distribution/reference"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmPlacementOnlyUpdatePreservesCompatibleTask(t *testing.T) {
	f := newExternalSwarmFixture(t)
	replicas := uint64(1)
	spec := dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{
			Name: f.stack + "_job",
			Labels: map[string]string{
				labelsdict.StackNamespace: f.stack,
				"com.docker.stack.image":  "busybox:1.36",
			},
		},
		TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Image: "busybox:1.36", Command: []string{"sleep", "600"}},
		},
		Mode: dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &replicas}},
	}
	applier := services.New(f.sw.Services, &registry.NopAuthManager{})
	apply := func(expected dockerswarm.ServiceSpec) {
		t.Helper()
		baseline, err := f.sw.Services.ListServices(f.ctx, swarm.ListServicesFilter{Stack: f.stack})
		require.NoError(t, err)
		applied, err := applier.Apply(f.ctx, f.stack, []dockerswarm.ServiceSpec{expected}, services.ApplyOptions{})
		require.NoError(t, err)
		require.Equal(t, []string{"job"}, applied.ChangedServices)
		result, err := waiter.NewServiceWaiter(f.sw.Services, f.sw.Nodes, 100*time.Millisecond, 30*time.Second).
			Wait(f.ctx, waiter.ServiceWaitRequest{
				StackName: f.stack, ServiceNames: []string{"job"}, Baseline: baseline,
				AppliedServices: applied.ChangedServices, ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": expected},
			})
		require.NoError(t, err)
		require.Empty(t, result.UnresolvedServices)
	}

	apply(spec)
	f.running(t, 1)
	before := f.tasks(t, "job")
	require.Len(t, before, 1)
	require.NotEmpty(t, before[0].Node)

	updated := f.inspect(t, "job").Spec
	updated.TaskTemplate.Placement = &dockerswarm.Placement{
		Constraints: []string{"node.id == " + before[0].Node},
	}
	apply(updated)

	after := f.tasks(t, "job")
	require.Len(t, after, 1)
	assert.Equal(t, before[0].ID, after[0].ID, "SwarmKit must preserve the compatible task")
	assert.Equal(t, before[0].CreatedAt, after[0].CreatedAt, "the preserved task keeps its creation generation")
	assert.Nil(t, f.inspect(t, "job").UpdateStatus, "placement-only preservation does not start a rolling update")
}

func TestRealSwarmMetadataUpdatePartialApplyRetry(t *testing.T) {
	f := newLifecycleFixture(t, false)
	load := func(label string, other bool) compose.File {
		services := fmt.Sprintf(`  a:
    image: busybox:1.36
    command: ["sleep", "600"]
    deploy:
      labels: {revision: %q}
`, label)
		if other {
			services += lifecycleService("b", "sleep 600", "")
		}
		file := f.manifest(t, services)
		// These regressions concern service state, so use host networking on
		// single-node Swarm runners that cannot create a VXLAN interface.
		file.Compose.Networks = map[string]compose.Network{"default": {External: true, Name: "host"}}
		return file
	}
	req := deployments.ExecuteRequest{
		Stack: config.StackSpec{Name: f.stack}, Commit: "initial", Desired: load("initial", false), SourceChanged: true,
	}
	result, err := f.executor.Execute(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, deploymentmodel.Succeeded, result.Status)
	before := f.settled(t, "a")
	initial := f.inspect(t, "a")

	req.Commit, req.Desired = "metadata-and-b", load("metadata", true)
	converted, err := converter.New(f.sw.Networks, f.sw.Configs, f.sw.Secrets).Convert(f.ctx, f.stack, req.Desired.Compose)
	require.NoError(t, err)
	// An existing, foreign-owned service makes B's application fail only after
	// the alphabetically earlier metadata-only update of A was accepted.
	foreign := converted.Services[1]
	foreign.Labels["com.docker.stack.namespace"] = f.stack + "-foreign"
	foreignID, err := f.sw.Services.Create(f.ctx, foreign, dockerswarm.ServiceCreateOptions{QueryRegistry: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = f.sw.Services.Remove(ctx, foreignID)
	})
	result, err = f.executor.Execute(f.ctx, req)
	require.ErrorContains(t, err, "not owned by stack")
	require.Equal(t, deploymentmodel.Failed, result.Status)
	metadata := f.inspect(t, "a")
	require.Equal(t, "metadata", metadata.Spec.Labels["revision"])
	require.Equal(t, initial.Spec.TaskTemplate, metadata.Spec.TaskTemplate)
	f.poll(t, "metadata-only update settled", func() bool {
		current := f.inspect(t, "a")
		return current.UpdateStatus == nil || current.UpdateStatus.State == dockerswarm.UpdateStateCompleted
	})
	t.Logf("Engine metadata-only update status: %v", f.inspect(t, "a").UpdateStatus)
	require.NoError(t, f.sw.Services.Remove(f.ctx, foreignID))
	req.ManualRetry = true
	result, err = f.executor.Execute(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, deploymentmodel.Succeeded, result.Status)
	require.Empty(t, f.latest(t).Verification.UnresolvedServices)
	after := f.settled(t, "a")
	assert.Equal(t, before.Service.Spec.TaskTemplate, after.Service.Spec.TaskTemplate)
	assert.Equal(t, before.Tasks, after.Tasks, "metadata-only retry must retain the original healthy tasks")
}

// authenticatedRegistry is a real distribution registry behind a BasicAuth gate.
// The gate records successful authenticated requests without storing credentials
// in test diagnostics, so a cached image cannot conceal stale Engine pull auth.
type authenticatedRegistry struct {
	host       string
	password   atomic.Value
	accepted   atomic.Uint64
	deny       atomic.Bool
	configPath string
}

func newAuthenticatedRegistry(t *testing.T, ctx context.Context) *authenticatedRegistry {
	t.Helper()
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	name := fmt.Sprintf("sd-registry-%d", time.Now().UnixNano())
	dockerFixtureCommand(t, ctx, "run", "--detach", "--name", name, "--publish", "127.0.0.1::5000", "registry:2")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		dockerFixtureCommand(t, cleanupCtx, "rm", "--force", name)
	})
	address := strings.TrimSpace(dockerFixtureCommand(t, ctx, "port", name, "5000/tcp"))
	target, err := url.Parse("http://" + address)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	r := &authenticatedRegistry{configPath: filepath.Join(os.Getenv("DOCKER_CONFIG"), "config.json")}
	r.password.Store("first-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if r.deny.Load() || !ok || username != "deployment" || password != r.password.Load().(string) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Swarm deployment E2E"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.accepted.Add(1)
		proxy.ServeHTTP(w, request)
	}))
	t.Cleanup(server.Close)
	r.host = strings.TrimPrefix(server.URL, "http://")
	r.rotate(t, "first-token")
	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v2/", nil)
		if err != nil {
			return false
		}
		request.SetBasicAuth("deployment", "first-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 20*time.Second, 100*time.Millisecond, "real registry must become ready")
	return r
}

func (r *authenticatedRegistry) rotate(t *testing.T, password string) {
	t.Helper()
	r.password.Store(password)
	config, err := json.Marshal(map[string]any{"auths": map[string]any{r.host: map[string]string{
		"auth": base64.StdEncoding.EncodeToString([]byte("deployment:" + password)),
	}}})
	require.NoError(t, err)
	// Rename the file, matching the documented directory-mount rotation path.
	require.NoError(t, os.WriteFile(r.configPath+".new", config, 0o600))
	require.NoError(t, os.Rename(r.configPath+".new", r.configPath))
}

func dockerFixtureCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	require.NoError(t, err, "Docker fixture command %v: %s", args, output)
	return string(output)
}

func evictRegistryFixtureReference(t *testing.T, f *externalSwarmFixture, tagged, resolved string) {
	t.Helper()
	shared, err := f.sw.Images.Get(f.ctx, "busybox:1.36")
	require.NoError(t, err)
	taggedName, err := reference.ParseNormalizedNamed(tagged)
	require.NoError(t, err)
	require.Equal(t, f.stack, reference.Path(taggedName), "evict only this fixture's private repository")
	resolvedName, err := reference.ParseNormalizedNamed(resolved)
	require.NoError(t, err)
	require.Equal(t, taggedName.Name(), resolvedName.Name(), "evict only this fixture's private repository")
	pinned, ok := resolvedName.(reference.Canonical)
	require.True(t, ok, "the service must use a resolved image")
	digestReference, err := reference.WithDigest(reference.TrimNamed(taggedName), pinned.Digest())
	require.NoError(t, err)
	dockerFixtureCommand(t, f.ctx, "image", "rm", "--force", tagged)
	if _, err = f.sw.Images.Get(f.ctx, resolved); !errors.Is(err, swarm.ErrImageNotFound) {
		require.NoError(t, err)
		// The containerd image store keeps the repository's digest reference
		// separately from its tag; the classic store may already have removed it.
		dockerFixtureCommand(t, f.ctx, "image", "rm", "--force", digestReference.String())
	}
	_, err = f.sw.Images.Get(f.ctx, resolved)
	require.ErrorIs(t, err, swarm.ErrImageNotFound)
	preserved, err := f.sw.Images.Get(f.ctx, "busybox:1.36")
	require.NoError(t, err)
	require.Equal(t, shared.ID, preserved.ID, "shared fixture image must remain available")
}

func registryManifest(t *testing.T, f *externalSwarmFixture, image string, replicas uint64, label string) compose.Compose {
	t.Helper()
	manifest := fmt.Sprintf(`services:
  private:
    image: %s
    command: ["sleep", "600"]
    deploy:
      replicas: %d
      labels: {revision: %q}
networks:
  default: {external: true, name: host}
`, image, replicas, label)
	require.NoError(t, os.WriteFile(f.path, []byte(manifest), 0o600))
	file, err := compose.NewFileLoader().Load(f.ctx, f.path)
	require.NoError(t, err)
	return file.Compose
}

func applyRegistryService(t *testing.T, f *externalSwarmFixture, applier *services.Applier, desired compose.Compose) {
	t.Helper()
	spec := registryServiceSpec(t, f, desired)
	baseline, err := f.sw.Services.ListServices(f.ctx, swarm.ListServicesFilter{Stack: f.stack})
	require.NoError(t, err)
	applied, err := applier.Apply(f.ctx, f.stack, []dockerswarm.ServiceSpec{spec}, services.ApplyOptions{})
	require.NoError(t, err)
	result, err := waiter.NewServiceWaiter(f.sw.Services, f.sw.Nodes, 100*time.Millisecond, 30*time.Second).Wait(f.ctx, waiter.ServiceWaitRequest{
		StackName: f.stack, ServiceNames: []string{"private"}, Baseline: baseline,
		AppliedServices: applied.ChangedServices, ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"private": spec},
	})
	require.NoError(t, err)
	require.Empty(t, result.UnresolvedServices)
}

func registryServiceSpec(t *testing.T, f *externalSwarmFixture, desired compose.Compose) dockerswarm.ServiceSpec {
	t.Helper()
	converted, err := converter.New(f.sw.Networks, f.sw.Configs, f.sw.Secrets).Convert(f.ctx, f.stack, desired)
	require.NoError(t, err)
	spec := converted.Services[0]
	if current, inspectErr := f.sw.Services.Inspect(f.ctx, spec.Name); inspectErr == nil {
		// Engine rewrites the built-in host attachment to a Swarm-internal ID.
		// Preserve that real attachment in this host-only fixture, so the auth
		// refresh is a true unchanged-spec operation.
		spec.TaskTemplate.Networks = current.Spec.TaskTemplate.Networks
	}
	return spec
}

func TestRealSwarmRegistryCredentialRotationWithoutRestart(t *testing.T) {
	f := newExternalSwarmFixtureWithTimeout(t, 3*time.Minute)
	r := newAuthenticatedRegistry(t, f.ctx)
	image := r.host + "/" + f.stack + ":private"
	dockerFixtureCommand(t, f.ctx, "tag", "busybox:1.36", image)
	dockerFixtureCommand(t, f.ctx, "push", image)
	applier := services.New(f.sw.Services, registry.NewAuthManager())
	desired := registryManifest(t, f, image, 1, "initial")
	applyRegistryService(t, f, applier, desired)
	before, err := f.observer.WaitStable(f.ctx, f.stack+"_private", time.Second)
	require.NoError(t, err)
	require.Contains(t, before.Service.Spec.TaskTemplate.ContainerSpec.Image, "@sha256:")
	r.rotate(t, "rotated-token")
	applyRegistryService(t, f, applier, desired)
	after, err := f.observer.WaitStable(f.ctx, f.stack+"_private", time.Second)
	require.NoError(t, err)
	require.Greater(t, after.Service.Version.Index, before.Service.Version.Index, "unchanged spec still receives rotated auth")
	require.Equal(t, before.Service.Spec.TaskTemplate, after.Service.Spec.TaskTemplate)
	require.Equal(t, before.Tasks, after.Tasks, "credential-only refresh must not restart tasks")
	f.unchanged(t, after)

	// The no-restart assertion above covers credential refresh. Now request an
	// external scale-down so both image stores can evict the private reference
	// without deleting an image still used by a running container.
	scale := func(replicas uint64) {
		t.Helper()
		f.poll(t, fmt.Sprintf("external scale to %d accepted", replicas), func() bool {
			current := f.inspect(t, "private")
			require.Equal(t, after.Service.ID, current.ID)
			current.Spec.Mode.Replicated.Replicas = &replicas
			return f.sw.Services.Update(f.ctx, current.ID, current.Version, current.Spec,
				dockerswarm.ServiceUpdateOptions{RegistryAuthFrom: dockerswarm.RegistryAuthFromSpec}) == nil
		})
	}
	scale(0)
	f.poll(t, "scaled-down tasks have stopped", func() bool {
		for _, task := range f.tasks(t, "private") {
			switch task.CurrentState {
			case swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed,
				swarm.TaskStateRejected, swarm.TaskStateRemove, swarm.TaskStateOrphaned:
			default:
				return false
			}
		}
		return true
	})
	// Keep the shared busybox image, but require the next task to contact the
	// registry. Both scale requests reuse Engine auth without resending a token.
	evictRegistryFixtureReference(t, f, image, after.Service.Spec.TaskTemplate.ContainerSpec.Image)
	acceptedBeforePull := r.accepted.Load()
	scale(1)
	f.poll(t, "new task pulled using stored rotated credentials", func() bool {
		current, err := f.observer.WaitRunningTasks(f.ctx, f.stack+"_private", 1)
		if err != nil || r.accepted.Load() <= acceptedBeforePull {
			return false
		}
		for _, task := range before.Tasks {
			for _, id := range current {
				if task.ID == id {
					return false
				}
			}
		}
		return true
	})
}

func TestRealSwarmMissingDigestRetriesResolution(t *testing.T) {
	f := newExternalSwarmFixtureWithTimeout(t, 3*time.Minute)
	r := newAuthenticatedRegistry(t, f.ctx)
	image := r.host + "/" + f.stack + ":private"
	dockerFixtureCommand(t, f.ctx, "tag", "busybox:1.36", image)
	dockerFixtureCommand(t, f.ctx, "push", image)
	applier := services.New(f.sw.Services, registry.NewAuthManager())
	r.deny.Store(true)
	desired := registryManifest(t, f, image, 0, "initial")
	applyRegistryService(t, f, applier, desired)
	unresolved := f.inspect(t, "private")
	require.Equal(t, image, unresolved.Spec.TaskTemplate.ContainerSpec.Image, "registry warning must leave the tag unresolved")
	r.deny.Store(false)
	desired = registryManifest(t, f, image, 0, "metadata-change")
	applyRegistryService(t, f, applier, desired)
	resolved := f.inspect(t, "private")
	require.Contains(t, resolved.Spec.TaskTemplate.ContainerSpec.Image, "@sha256:")
	require.Zero(t, *resolved.Spec.Mode.Replicated.Replicas)
	assert.Empty(t, f.tasks(t, "private"), "resolution retries cannot start a zero-replica service")
}

func TestRealSwarmCredentialRotationDoesNotRerunCompletedJobs(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job", "replicated-none", "replicated-on-failure", "external"} {
		t.Run(mode, func(t *testing.T) {
			f := newExternalSwarmFixtureWithTimeout(t, 3*time.Minute)
			r := newAuthenticatedRegistry(t, f.ctx)
			image := r.host + "/" + f.stack + ":private"
			dockerFixtureCommand(t, f.ctx, "tag", "busybox:1.36", image)
			dockerFixtureCommand(t, f.ctx, "push", image)
			desired := registryManifest(t, f, image, 1, "initial")
			desired.Services[0].Command = compose.NewCommand([]string{"true"})
			desired.Services[0].Deploy.RestartPolicy = &compose.ServiceDeployRestartPolicy{Condition: "none"}
			switch mode {
			case "replicated-job", "global-job":
				desired.Services[0].Deploy.Mode = mode
				if mode == "global-job" {
					desired.Services[0].Deploy.Replicas = nil
				}
			case "replicated-on-failure":
				desired.Services[0].Deploy.RestartPolicy.Condition = "on-failure"
			case "external":
				desired.Services[0].Deploy.Labels.Map["org.swarm-deploy.service.sync.policy.replicasManaged"] = "false"
			}
			applier := services.New(f.sw.Services, registry.NewAuthManager())
			apply := func() services.ApplyResult {
				t.Helper()
				result, err := applier.Apply(f.ctx, f.stack,
					[]dockerswarm.ServiceSpec{registryServiceSpec(t, f, desired)}, services.ApplyOptions{})
				require.NoError(t, err)
				return result
			}
			apply()
			f.poll(t, "initial one-shot execution completed", func() bool {
				tasks := f.tasks(t, "private")
				return len(tasks) == 1 && tasks[0].CurrentState == swarm.TaskStateComplete
			})
			before, err := f.observer.WaitStable(f.ctx, f.stack+"_private", time.Second)
			require.NoError(t, err)
			r.rotate(t, "rotated-token")
			require.Empty(t, apply().ChangedServices, "auth-only update must not create another job iteration")
			applier = services.New(f.sw.Services, registry.NewAuthManager())
			require.Empty(t, apply().ChangedServices, "unknown auth after restart must not rerun completed work")
			require.NoError(t, f.observer.WaitUnchanged(f.ctx, before, time.Second))

			// The next requested template change must still carry the new token.
			evictRegistryFixtureReference(t, f, image, before.Service.Spec.TaskTemplate.ContainerSpec.Image)
			acceptedBeforePull := r.accepted.Load()
			desired.Services[0].Command = compose.NewCommand([]string{"sh", "-c", "echo requested-execution"})
			require.Equal(t, []string{"private"}, apply().ChangedServices)
			if mode == "external" {
				parked := f.inspect(t, "private")
				require.Zero(t, *parked.Spec.Mode.Replicated.Replicas)
				f.poll(t, "external scheduler requested next execution", func() bool {
					current := f.inspect(t, "private")
					one := uint64(1)
					current.Spec.Mode.Replicated.Replicas = &one
					current.Spec.TaskTemplate.ForceUpdate++
					return f.sw.Services.Update(f.ctx, current.ID, current.Version, current.Spec,
						dockerswarm.ServiceUpdateOptions{RegistryAuthFrom: dockerswarm.RegistryAuthFromSpec}) == nil
				})
			}
			f.poll(t, "requested new execution pulled with rotated credentials", func() bool {
				if r.accepted.Load() <= acceptedBeforePull {
					return false
				}
				for _, task := range f.tasks(t, "private") {
					if task.ID != before.Tasks[0].ID && task.CurrentState == swarm.TaskStateComplete {
						return true
					}
				}
				return false
			})
		})
	}
}
